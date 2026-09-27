package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"nscramble-server/internal/store"
)

const testKey = "test-key-0123456789"

func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return New(st, testKey, time.UTC, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func solve(n int, edit ...func(map[string]any)) map[string]any {
	s := map[string]any{
		"id":         fmt.Sprintf("0199a7c1-0000-7000-8000-%012x", n),
		"created_at": 1_790_000_000_000 + n,
		"date":       "2026-09-26",
		"time_ms":    10_000 + n,
		"scramble":   "R U R' U'",
		"penalty":    0,
		"updated_at": 1_790_000_000_000 + n,
		"deleted_at": nil,
	}
	for _, e := range edit {
		e(s)
	}
	return s
}

type syncResponse struct {
	Rev     int64            `json:"rev"`
	More    bool             `json:"more"`
	Changes []map[string]any `json:"changes"`
	Error   string           `json:"error"`
}

func call(t *testing.T, h http.Handler, body any, key string) (int, syncResponse) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var resp syncResponse
	json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec.Code, resp
}

func doSync(t *testing.T, h http.Handler, since int64, changes ...map[string]any) syncResponse {
	t.Helper()
	if changes == nil {
		changes = []map[string]any{}
	}
	code, resp := call(t, h, map[string]any{"since": since, "changes": changes}, testKey)
	if code != http.StatusOK {
		t.Fatalf("sync: status %d: %s", code, resp.Error)
	}
	return resp
}

// normalize round-trips through JSON so expected maps compare equal to decoded responses.
func normalize(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	json.Unmarshal(raw, &out)
	return out
}

func ids(changes []map[string]any) []string {
	out := []string{}
	for _, c := range changes {
		out = append(out, c["id"].(string))
	}
	return out
}

func TestAuth(t *testing.T) {
	h := newTestServer(t)
	if code, _ := call(t, h, map[string]any{"since": 0, "changes": []any{}}, "wrong-key"); code != http.StatusUnauthorized {
		t.Errorf("wrong key: got %d", code)
	}
	req := httptest.NewRequest(http.MethodPost, "/sync", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no key: got %d", rec.Code)
	}
}

func TestHealthNeedsNoKey(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestServer(t).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health: got %d", rec.Code)
	}
}

func TestPushThenPullFromAnotherDevice(t *testing.T) {
	h := newTestServer(t)
	if got := doSync(t, h, 0, solve(1), solve(2)); got.Rev != 2 {
		t.Fatalf("rev after push = %d, want 2", got.Rev)
	}
	other := doSync(t, h, 0)
	if !reflect.DeepEqual(normalize(other.Changes), normalize([]any{solve(1), solve(2)})) || other.More {
		t.Fatalf("pull = %+v", other)
	}
	if again := doSync(t, h, other.Rev); again.Rev != 2 || len(again.Changes) != 0 {
		t.Fatalf("nothing new = %+v", again)
	}
}

func TestNewerEditWinsOlderIsIgnored(t *testing.T) {
	h := newTestServer(t)
	base := solve(1)["updated_at"].(int)
	doSync(t, h, 0, solve(1))
	doSync(t, h, 0, solve(1, func(s map[string]any) { s["penalty"] = 2; s["updated_at"] = base + 100 }))
	doSync(t, h, 0, solve(1, func(s map[string]any) { s["penalty"] = 1; s["updated_at"] = base + 50 }))

	got := doSync(t, h, 0)
	if len(got.Changes) != 1 || got.Changes[0]["penalty"] != float64(2) {
		t.Fatalf("got %+v", got.Changes)
	}
}

func TestAcceptedEditGetsNewRevisionRejectedDoesNot(t *testing.T) {
	h := newTestServer(t)
	doSync(t, h, 0, solve(1), solve(2))
	edited := doSync(t, h, 2, solve(1, func(s map[string]any) {
		s["deleted_at"] = 5
		s["updated_at"] = s["updated_at"].(int) + 1
	}))
	if edited.Rev != 3 || !reflect.DeepEqual(ids(edited.Changes), []string{solve(1)["id"].(string)}) {
		t.Fatalf("edited = %+v", edited)
	}
	if stale := doSync(t, h, 3, solve(2)); stale.Rev != 3 || len(stale.Changes) != 0 {
		t.Fatalf("stale = %+v", stale)
	}
}

func TestPagesLargePulls(t *testing.T) {
	h := newTestServer(t)
	var all []map[string]any
	for i := 1; i <= PageSize+20; i++ {
		all = append(all, solve(i))
	}
	doSync(t, h, 0, all[:MaxPush]...)
	doSync(t, h, 0, all[MaxPush:]...)

	first := doSync(t, h, 0)
	if len(first.Changes) != PageSize || !first.More {
		t.Fatalf("first page: %d changes, more=%v", len(first.Changes), first.More)
	}
	second := doSync(t, h, first.Rev)
	if len(second.Changes) != 20 || second.More {
		t.Fatalf("second page: %d changes, more=%v", len(second.Changes), second.More)
	}
}

func TestMissingDeletedAtMeansNotDeleted(t *testing.T) {
	h := newTestServer(t)
	got := doSync(t, h, 0, solve(1, func(s map[string]any) { delete(s, "deleted_at") }))
	if len(got.Changes) != 1 || got.Changes[0]["deleted_at"] != nil {
		t.Fatalf("got %+v", got.Changes)
	}
}

func TestRejectsInvalidInput(t *testing.T) {
	h := newTestServer(t)
	cases := map[string]any{
		"negative since":   map[string]any{"since": -1, "changes": []any{}},
		"fractional since": map[string]any{"since": 1.5, "changes": []any{}},
		"missing changes":  map[string]any{"since": 0},
		"bad penalty":      map[string]any{"since": 0, "changes": []any{solve(1, func(s map[string]any) { s["penalty"] = 3 })}},
		"bad id":           map[string]any{"since": 0, "changes": []any{solve(1, func(s map[string]any) { s["id"] = "not-a-uuid" })}},
		"bad date":         map[string]any{"since": 0, "changes": []any{solve(1, func(s map[string]any) { s["date"] = "26/09/2026" })}},
		"missing field":    map[string]any{"since": 0, "changes": []any{solve(1, func(s map[string]any) { delete(s, "time_ms") })}},
		"too many":         map[string]any{"since": 0, "changes": make([]any, MaxPush+1)},
	}
	for name, body := range cases {
		if code, _ := call(t, h, body, testKey); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/sync", nil)
	req.Header.Set("Authorization", "Bearer "+testKey)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /sync: got %d, want 405", rec.Code)
	}
}

func TestBatchWithInvalidSolveStoresNothing(t *testing.T) {
	h := newTestServer(t)
	call(t, h, map[string]any{"since": 0, "changes": []any{solve(1), solve(2, func(s map[string]any) { s["time_ms"] = -5 })}}, testKey)
	if got := doSync(t, h, 0); len(got.Changes) != 0 {
		t.Fatalf("stored %d solves", len(got.Changes))
	}
}
