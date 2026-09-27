package server

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nscramble-server/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStatsServer(t *testing.T) (http.Handler, *clock) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	return newServer(st, Options{APIKey: testKey, Location: time.UTC}, slog.New(slog.NewTextHandler(io.Discard, nil)), c.now), c
}

func getStats(t *testing.T, h http.Handler) (statsResponse, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stats", nil)) // no API key
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stats: %d %s", rec.Code, rec.Body.String())
	}
	var resp statsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp, rec
}

// solveOn makes solve n on the given day with a time and penalty.
func solveOn(n int, date string, ms int, penalty int) map[string]any {
	return solve(n, func(s map[string]any) {
		s["date"] = date
		s["time_ms"] = ms
		s["penalty"] = penalty
	})
}

func TestStatsEmpty(t *testing.T) {
	h, _ := newStatsServer(t)
	resp, rec := getStats(t, h)
	if resp.RecentSession != nil || len(resp.AverageHistory) != 0 || len(resp.SolveCountHistory) != 0 {
		t.Fatalf("got %+v", resp)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}
}

func TestStatsRecentSessionAndHistory(t *testing.T) {
	h, _ := newStatsServer(t)
	doSync(t, h, 0,
		solveOn(1, "2026-09-20", 20_000, 0),
		solveOn(2, "2026-09-20", 22_000, 0),
		solveOn(3, "2026-09-20", 24_000, 0),
		// Most recent day with solves: 10, 12, 11, 9, 14 s, a +2 on 12 s (= 14), a DNF, and a deleted solve.
		solveOn(4, "2026-09-26", 10_000, 0),
		solveOn(5, "2026-09-26", 12_000, 0),
		solveOn(6, "2026-09-26", 11_000, 0),
		solveOn(7, "2026-09-26", 9_000, 0),
		solveOn(8, "2026-09-26", 12_000, 1),
		solveOn(9, "2026-09-26", 30_000, 2),
		solve(10, func(s map[string]any) { s["date"] = "2026-09-26"; s["time_ms"] = 1; s["deleted_at"] = 1 }),
	)
	resp, _ := getStats(t, h)

	rs := resp.RecentSession
	if rs == nil || rs.Date != "2026-09-26" || rs.Solves != 6 {
		t.Fatalf("recent session = %+v", rs)
	}
	// Sorted: 9, 10, 11, 12, 14, DNF -> trim 1 each side -> 10, 11, 12, 14 -> 11.75.
	if val(rs.AverageMs) != int64(11_750) {
		t.Errorf("average = %v", val(rs.AverageMs))
	}
	// Median of 9, 10, 11, 12, 14, DNF = (11 + 12) / 2.
	if val(rs.MedianMs) != int64(11_500) {
		t.Errorf("median = %v", val(rs.MedianMs))
	}
	// σ of 9, 10, 11, 12, 14 (DNF excluded) = 1.7205 s -> 1.72.
	if val(rs.StdDevMs) != int64(1_720) {
		t.Errorf("std dev = %v", val(rs.StdDevMs))
	}

	if len(resp.AverageHistory) != 2 || resp.AverageHistory[0].Date != "2026-09-20" ||
		val(resp.AverageHistory[0].AverageMs) != int64(22_000) || val(resp.AverageHistory[1].AverageMs) != int64(11_750) {
		t.Errorf("average history = %+v", resp.AverageHistory)
	}
	if len(resp.SolveCountHistory) != 2 || resp.SolveCountHistory[0].Solves != 3 || resp.SolveCountHistory[1].Solves != 6 {
		t.Errorf("solve count history = %+v", resp.SolveCountHistory)
	}
}

func TestStatsHistoryIsLast30SolveDates(t *testing.T) {
	h, _ := newStatsServer(t)
	var changes []map[string]any
	for day := 1; day <= 35; day++ {
		changes = append(changes, solveOn(day, time.Date(2026, 8, day, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), 10_000, 0))
	}
	doSync(t, h, 0, changes...)
	resp, _ := getStats(t, h)
	if len(resp.AverageHistory) != 30 || resp.AverageHistory[0].Date != "2026-08-06" || resp.AverageHistory[29].Date != "2026-09-04" {
		t.Fatalf("got %d days from %s to %s", len(resp.AverageHistory), resp.AverageHistory[0].Date, resp.AverageHistory[len(resp.AverageHistory)-1].Date)
	}
	if resp.AverageHistory[0].AverageMs != nil {
		t.Error("a single solve has no average")
	}
}

func TestStatsAreCachedForAMinute(t *testing.T) {
	h, c := newStatsServer(t)
	doSync(t, h, 0, solveOn(1, "2026-09-26", 10_000, 0))
	first, _ := getStats(t, h)

	doSync(t, h, 0, solveOn(2, "2026-09-26", 11_000, 0))
	c.t = c.t.Add(59 * time.Second)
	if cached, _ := getStats(t, h); cached.RecentSession.Solves != 1 || !cached.GeneratedAt.Equal(first.GeneratedAt) {
		t.Fatalf("within TTL: %+v", cached.RecentSession)
	}

	c.t = c.t.Add(2 * time.Second)
	if fresh, _ := getStats(t, h); fresh.RecentSession.Solves != 2 {
		t.Fatalf("after TTL: %+v", fresh.RecentSession)
	}
}

func val(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestRecentSessionSkipsToday(t *testing.T) {
	h, c := newStatsServer(t) // now: 2026-09-27 12:00 UTC
	doSync(t, h, 0,
		solveOn(1, "2026-09-25", 10_000, 0),
		solveOn(2, "2026-09-27", 20_000, 0), // today: in progress, not the recent session
	)
	resp, _ := getStats(t, h)
	if resp.RecentSession == nil || resp.RecentSession.Date != "2026-09-25" || resp.RecentSession.Solves != 1 {
		t.Fatalf("recent session = %+v", resp.RecentSession)
	}
	if len(resp.SolveCountHistory) != 2 {
		t.Errorf("history should still include today: %+v", resp.SolveCountHistory)
	}

	// Only today has solves: no recent session yet.
	h2, _ := newStatsServer(t)
	doSync(t, h2, 0, solveOn(3, "2026-09-27", 20_000, 0))
	if resp, _ := getStats(t, h2); resp.RecentSession != nil {
		t.Fatalf("recent session = %+v", resp.RecentSession)
	}

	// "Today" follows the configured time zone: 23:30 UTC is already the 28th in Budapest.
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tz.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	budapest, err := time.LoadLocation("Europe/Budapest")
	if err != nil {
		t.Fatal(err)
	}
	c.t = time.Date(2026, 9, 27, 23, 30, 0, 0, time.UTC)
	h3 := newServer(st, Options{APIKey: testKey, Location: budapest}, slog.New(slog.NewTextHandler(io.Discard, nil)), c.now)
	doSync(t, h3, 0, solveOn(4, "2026-09-27", 20_000, 0))
	if resp, _ := getStats(t, h3); resp.RecentSession == nil || resp.RecentSession.Date != "2026-09-27" {
		t.Fatalf("recent session = %+v", resp.RecentSession)
	}
}
