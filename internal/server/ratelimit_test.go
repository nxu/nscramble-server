package server

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"nscramble-server/internal/store"
)

func newLimitedServer(t *testing.T, behindProxy bool) (http.Handler, *clock) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{t: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)}
	opts := Options{APIKey: testKey, Location: time.UTC, BehindProxy: behindProxy}
	return newServer(st, opts, slog.New(slog.NewTextHandler(io.Discard, nil)), c.now), c
}

// attempt posts an empty sync from remoteAddr (and optional X-Forwarded-For) and returns the response.
func attempt(h http.Handler, key, remoteAddr, forwardedFor string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader([]byte(`{"since":0,"changes":[]}`)))
	req.Header.Set("Authorization", "Bearer "+key)
	req.RemoteAddr = remoteAddr
	if forwardedFor != "" {
		req.Header.Set("X-Forwarded-For", forwardedFor)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestBadKeysGetClientBlocked(t *testing.T) {
	h, c := newLimitedServer(t, false)
	const attacker = "203.0.113.7:4000"
	for i := 0; i < maxAuthFailures; i++ {
		if code := attempt(h, "guess", attacker, "").Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, code)
		}
	}
	// Blocked now: even the right key is refused, so guessing can't continue.
	rec := attempt(h, testKey, attacker, "")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "900" {
		t.Fatalf("blocked: %d, Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	// Other clients are unaffected.
	if code := attempt(h, testKey, "198.51.100.1:5000", "").Code; code != http.StatusOK {
		t.Fatalf("other client: %d", code)
	}
	// The block expires.
	c.t = c.t.Add(authBlockDuration)
	if code := attempt(h, testKey, attacker, "").Code; code != http.StatusOK {
		t.Fatalf("after block: %d", code)
	}
}

func TestFailuresExpireAndSuccessResets(t *testing.T) {
	h, c := newLimitedServer(t, false)
	const client = "203.0.113.8:4000"
	for i := 0; i < maxAuthFailures-1; i++ {
		attempt(h, "typo", client, "")
	}
	// Outside the window the count starts over.
	c.t = c.t.Add(authFailureWindow + time.Second)
	attempt(h, "typo", client, "")
	if code := attempt(h, testKey, client, "").Code; code != http.StatusOK {
		t.Fatalf("after window: %d", code)
	}
	// A success clears the count.
	for i := 0; i < maxAuthFailures-1; i++ {
		attempt(h, "typo", client, "")
	}
	attempt(h, testKey, client, "")
	for i := 0; i < maxAuthFailures-1; i++ {
		attempt(h, "typo", client, "")
	}
	if code := attempt(h, testKey, client, "").Code; code != http.StatusOK {
		t.Fatalf("after reset: %d", code)
	}
}

func TestBehindProxyUsesForwardedFor(t *testing.T) {
	const proxy = "10.0.0.1:443"
	direct, _ := newLimitedServer(t, false)
	for i := 0; i < maxAuthFailures; i++ {
		attempt(direct, "guess", proxy, "203.0.113.9")
	}
	// Without BehindProxy the header is ignored: everyone behind the proxy shares its address.
	if code := attempt(direct, testKey, proxy, "198.51.100.2").Code; code != http.StatusTooManyRequests {
		t.Fatalf("direct: %d", code)
	}

	proxied, _ := newLimitedServer(t, true)
	for i := 0; i < maxAuthFailures; i++ {
		// A client-supplied first entry doesn't help: the proxy-appended last entry counts.
		attempt(proxied, "guess", proxy, "1.2.3.4, 203.0.113.9")
	}
	if code := attempt(proxied, testKey, proxy, "203.0.113.9").Code; code != http.StatusTooManyRequests {
		t.Fatalf("attacker behind proxy: %d", code)
	}
	if code := attempt(proxied, testKey, proxy, "198.51.100.2").Code; code != http.StatusOK {
		t.Fatalf("other client behind proxy: %d", code)
	}
}

func TestLimiterSweepsExpiredEntries(t *testing.T) {
	l := authLimiter{clients: map[string]*authFailures{}}
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	for _, ip := range []string{"a", "b", "c"} {
		l.fail(ip, now)
	}
	l.fail("d", now.Add(authFailureWindow+2*time.Minute))
	if len(l.clients) != 1 {
		t.Fatalf("clients after sweep: %d", len(l.clients))
	}
}
