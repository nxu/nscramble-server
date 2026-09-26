// Package server is the HTTP API: GET /health and POST /sync (see README for the protocol).
package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"nscramble-server/internal/store"
)

const (
	// MaxPush is the most solves accepted in one request.
	MaxPush = 500
	// PageSize is the most solves returned in one response; "more" tells the client to ask again.
	PageSize = 500
	// maxBodyBytes comfortably fits MaxPush solves.
	maxBodyBytes = 4 << 20
)

type Server struct {
	store      *store.Store
	apiKeyHash [32]byte
	log        *slog.Logger
}

// New returns the HTTP handler. apiKey is the pre-shared secret clients send as a bearer token.
func New(st *store.Store, apiKey string, log *slog.Logger) http.Handler {
	s := &Server{store: st, apiKeyHash: sha256.Sum256([]byte(apiKey)), log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.Handle("POST /sync", s.requireAPIKey(http.HandlerFunc(s.handleSync)))
	return s.logRequests(mux)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "database unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) requireAPIKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		// Compare hashes so the comparison takes the same time whatever the key's length.
		given := sha256.Sum256([]byte(key))
		if !ok || subtle.ConstantTimeCompare(given[:], s.apiKeyHash[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Wire format. Pointers tell missing fields apart from zero values.
type syncRequest struct {
	Since   *int64       `json:"since"`
	Changes *[]wireSolve `json:"changes"`
}

type wireSolve struct {
	ID        *string `json:"id"`
	CreatedAt *int64  `json:"created_at"`
	Date      *string `json:"date"`
	TimeMs    *int64  `json:"time_ms"`
	Scramble  *string `json:"scramble"`
	Penalty   *int    `json:"penalty"`
	UpdatedAt *int64  `json:"updated_at"`
	DeletedAt *int64  `json:"deleted_at"` // null or missing: not deleted
}

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	dayPattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	var req syncRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes)).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Since == nil || *req.Since < 0 {
		writeError(w, http.StatusBadRequest, "since must be a non-negative integer")
		return
	}
	if req.Changes == nil {
		writeError(w, http.StatusBadRequest, "changes must be an array")
		return
	}
	if len(*req.Changes) > MaxPush {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("at most %d changes per request", MaxPush))
		return
	}
	changes := make([]store.Solve, 0, len(*req.Changes))
	for i, c := range *req.Changes {
		solve, ok := c.validate()
		if !ok {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid solve at index %d", i))
			return
		}
		changes = append(changes, solve)
	}

	result, err := s.store.Sync(r.Context(), *req.Since, changes, PageSize)
	if err != nil {
		s.log.Error("sync failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	s.log.Info("sync", "pushed", len(changes), "pulled", len(result.Changes), "rev", result.Rev)
	writeJSON(w, http.StatusOK, result)
}

func (c wireSolve) validate() (store.Solve, bool) {
	nonNegative := func(v *int64) bool { return v != nil && *v >= 0 }
	valid := c.ID != nil && uuidPattern.MatchString(*c.ID) &&
		nonNegative(c.CreatedAt) &&
		c.Date != nil && dayPattern.MatchString(*c.Date) &&
		nonNegative(c.TimeMs) &&
		c.Scramble != nil && len(*c.Scramble) <= 200 &&
		c.Penalty != nil && *c.Penalty >= 0 && *c.Penalty <= 2 &&
		nonNegative(c.UpdatedAt) &&
		(c.DeletedAt == nil || *c.DeletedAt >= 0)
	if !valid {
		return store.Solve{}, false
	}
	return store.Solve{
		ID:        *c.ID,
		CreatedAt: *c.CreatedAt,
		Date:      *c.Date,
		TimeMs:    *c.TimeMs,
		Scramble:  *c.Scramble,
		Penalty:   *c.Penalty,
		UpdatedAt: *c.UpdatedAt,
		DeletedAt: c.DeletedAt,
	}, true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.log.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"duration_ms", time.Since(start).Milliseconds())
	})
}
