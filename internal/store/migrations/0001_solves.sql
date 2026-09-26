-- Same columns as the app's local `solves` table (nscramble-app, Packages/Storage),
-- plus `rev`: a server-assigned, strictly increasing revision used as the sync cursor.
CREATE TABLE solves (
    id TEXT PRIMARY KEY NOT NULL,
    created_at INTEGER NOT NULL,
    date TEXT NOT NULL,
    time_ms INTEGER NOT NULL,
    scramble TEXT NOT NULL,
    penalty INTEGER NOT NULL DEFAULT 0 CHECK (penalty IN (0, 1, 2)),
    updated_at INTEGER NOT NULL,
    deleted_at INTEGER,
    rev INTEGER NOT NULL
);
CREATE UNIQUE INDEX solves_rev ON solves(rev);
CREATE INDEX solves_date ON solves(date);
