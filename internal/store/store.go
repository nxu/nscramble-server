// Package store keeps solves in SQLite and implements the sync protocol's storage side.
package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite" // pure Go: no cgo, so the binary is fully static
)

//go:embed migrations/*.sql
var migrations embed.FS

// Solve mirrors the app's solves table. Times are Unix epoch milliseconds.
type Solve struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Date      string `json:"date"` // local calendar day on the solving device, YYYY-MM-DD
	TimeMs    int64  `json:"time_ms"`
	Scramble  string `json:"scramble"`
	Penalty   int    `json:"penalty"` // 0 none, 1 +2, 2 DNF
	UpdatedAt int64  `json:"updated_at"`
	DeletedAt *int64 `json:"deleted_at"`
}

// SyncResult is what the server returns: solves changed after the client's cursor.
type SyncResult struct {
	Rev     int64   `json:"rev"`
	More    bool    `json:"more"`
	Changes []Solve `json:"changes"`
}

type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection serializes syncs, which keeps revision assignment trivially correct.
	// Traffic is one person's devices, so there's nothing to gain from more.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// migrate applies migrations/NNNN_*.sql files newer than PRAGMA user_version, each in a transaction.
func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		name := strings.TrimPrefix(file, "migrations/")
		n, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %q", name)
		}
		if n <= version {
			continue
		}
		sqlText, err := migrations.ReadFile(file)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlText)); err != nil {
			tx.Rollback()
			return fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", n)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Last write wins: an incoming solve replaces the stored one only if it was edited later.
const upsertSQL = `
INSERT INTO solves (id, created_at, date, time_ms, scramble, penalty, updated_at, deleted_at, rev)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (id) DO UPDATE SET
	created_at = excluded.created_at,
	date = excluded.date,
	time_ms = excluded.time_ms,
	scramble = excluded.scramble,
	penalty = excluded.penalty,
	updated_at = excluded.updated_at,
	deleted_at = excluded.deleted_at,
	rev = excluded.rev
WHERE excluded.updated_at > solves.updated_at`

const pullSQL = `
SELECT id, created_at, date, time_ms, scramble, penalty, updated_at, deleted_at, rev
FROM solves WHERE rev > ? ORDER BY rev LIMIT ?`

// Sync stores changes (last write wins, each accepted change getting the next revision), then
// returns up to pageSize solves with a revision above since, in revision order. All in one transaction.
func (s *Store) Sync(ctx context.Context, since int64, changes []Solve, pageSize int) (SyncResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return SyncResult{}, err
	}
	defer tx.Rollback()

	if len(changes) > 0 {
		var next int64
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(rev), 0) + 1 FROM solves").Scan(&next); err != nil {
			return SyncResult{}, err
		}
		stmt, err := tx.PrepareContext(ctx, upsertSQL)
		if err != nil {
			return SyncResult{}, err
		}
		defer stmt.Close()
		for _, c := range changes {
			res, err := stmt.ExecContext(ctx,
				c.ID, c.CreatedAt, c.Date, c.TimeMs, c.Scramble, c.Penalty, c.UpdatedAt, c.DeletedAt, next)
			if err != nil {
				return SyncResult{}, fmt.Errorf("store %s: %w", c.ID, err)
			}
			if n, err := res.RowsAffected(); err != nil {
				return SyncResult{}, err
			} else if n > 0 {
				next++
			}
		}
	}

	rows, err := tx.QueryContext(ctx, pullSQL, since, pageSize+1)
	if err != nil {
		return SyncResult{}, err
	}
	defer rows.Close()
	result := SyncResult{Rev: since, Changes: []Solve{}}
	for rows.Next() {
		var sv Solve
		var rev int64
		if err := rows.Scan(&sv.ID, &sv.CreatedAt, &sv.Date, &sv.TimeMs, &sv.Scramble, &sv.Penalty,
			&sv.UpdatedAt, &sv.DeletedAt, &rev); err != nil {
			return SyncResult{}, err
		}
		if len(result.Changes) == pageSize {
			result.More = true
			break
		}
		result.Changes = append(result.Changes, sv)
		result.Rev = rev
	}
	if err := rows.Err(); err != nil {
		return SyncResult{}, err
	}
	if err := rows.Close(); err != nil {
		return SyncResult{}, err
	}
	return result, tx.Commit()
}
