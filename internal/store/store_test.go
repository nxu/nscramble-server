package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReopenKeepsDataAndSkipsAppliedMigrations(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")

	st, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	solve := Solve{ID: "0199a7c1-0000-7000-8000-000000000001", CreatedAt: 1, Date: "2026-09-26", TimeMs: 9_000, Scramble: "R", UpdatedAt: 1}
	if _, err := st.Sync(ctx, 0, []Solve{solve}, 10); err != nil {
		t.Fatal(err)
	}
	st.Close()

	st, err = Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()
	got, err := st.Sync(ctx, 0, nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Changes) != 1 || got.Changes[0] != solve || got.Rev != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestDeletedAtRoundTrips(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	deleted := int64(42)
	solve := Solve{ID: "0199a7c1-0000-7000-8000-000000000001", Date: "2026-09-26", Penalty: 2, UpdatedAt: 1, DeletedAt: &deleted}
	got, err := st.Sync(ctx, 0, []Solve{solve}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Changes[0].DeletedAt == nil || *got.Changes[0].DeletedAt != 42 || got.Changes[0].Penalty != 2 {
		t.Fatalf("got %+v", got.Changes[0])
	}
}
