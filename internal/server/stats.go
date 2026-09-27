package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"nscramble-server/internal/stats"
)

const (
	// historyDays is how many solve dates the history arrays cover.
	historyDays = 30
	// statsTTL is how long a computed /stats response is reused.
	statsTTL = time.Minute
)

// statsResponse is the public GET /stats body. Times are milliseconds; null means a DNF result or
// too few solves. Dates are the solving device's local calendar days (YYYY-MM-DD).
type statsResponse struct {
	GeneratedAt       time.Time     `json:"generated_at"`
	RecentSession     *sessionStats `json:"recent_session"` // latest day with solves before today; null if none
	AverageHistory    []dateAverage `json:"average_history"`
	SolveCountHistory []dateCount   `json:"solve_count_history"`
}

type sessionStats struct {
	Date      string `json:"date"`
	Solves    int    `json:"solves"`
	AverageMs *int64 `json:"average_ms"`
	MedianMs  *int64 `json:"median_ms"`
	StdDevMs  *int64 `json:"std_dev_ms"`
}

type dateAverage struct {
	Date      string `json:"date"`
	AverageMs *int64 `json:"average_ms"`
}

type dateCount struct {
	Date   string `json:"date"`
	Solves int    `json:"solves"`
}

// statsCache holds the last /stats body. The mutex is held while computing, so a burst of
// requests after expiry computes once and the rest wait for that result.
type statsCache struct {
	mu        sync.Mutex
	body      []byte
	expiresAt time.Time
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	s.statsCache.mu.Lock()
	body := s.statsCache.body
	if body == nil || !s.now().Before(s.statsCache.expiresAt) {
		var err error
		body, err = s.computeStats(r)
		if err != nil {
			s.statsCache.mu.Unlock()
			s.log.Error("stats failed", "err", err)
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}
		s.statsCache.body = body
		s.statsCache.expiresAt = s.now().Add(statsTTL)
	}
	s.statsCache.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.Write(body)
}

func (s *Server) computeStats(r *http.Request) ([]byte, error) {
	days, err := s.store.RecentDays(r.Context(), historyDays)
	if err != nil {
		return nil, err
	}
	resp := statsResponse{
		GeneratedAt:       s.now().UTC(),
		AverageHistory:    []dateAverage{},
		SolveCountHistory: []dateCount{},
	}
	for _, d := range days {
		resp.AverageHistory = append(resp.AverageHistory, dateAverage{Date: d.Date, AverageMs: stats.Average(d.Results)})
		resp.SolveCountHistory = append(resp.SolveCountHistory, dateCount{Date: d.Date, Solves: len(d.Results)})
	}
	// The recent session is the latest completed day: skip today, which may still be in progress.
	today := s.now().In(s.location).Format("2006-01-02")
	for i := len(days) - 1; i >= 0; i-- {
		last := days[i]
		if last.Date >= today {
			continue
		}
		resp.RecentSession = &sessionStats{
			Date:      last.Date,
			Solves:    len(last.Results),
			AverageMs: stats.Average(last.Results),
			MedianMs:  stats.Median(last.Results),
			StdDevMs:  stats.StdDev(last.Results),
		}
		break
	}
	return json.Marshal(resp)
}
