// Package store provides the SQLite persistence layer for the wha
// (wallbox-homeautomation) controller: charge sessions, an audit event log,
// and time-series samples used for charts.
//
// It uses the pure-Go modernc.org/sqlite driver (no cgo) so the binary stays
// statically linkable for arm64 targets. wha is the sole writer to this
// database, so the connection pool is capped at a single connection to avoid
// "database is locked" errors.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registers driver name "sqlite"
)

// tsLayout is a fixed-width RFC3339 layout (nanosecond precision) used to store
// timestamps as TEXT. Because every timestamp is stored in UTC the trailing
// offset is always "Z", which keeps the encoding fixed-width and therefore
// lexicographically sortable — range and ORDER BY queries on timestamp columns
// behave chronologically.
const tsLayout = "2006-01-02T15:04:05.000000000Z07:00"

// formatTime renders t as a UTC, fixed-width timestamp string for storage.
func formatTime(t time.Time) string {
	return t.UTC().Format(tsLayout)
}

// parseTime parses a stored timestamp string back into a UTC time.Time.
//
// We parse with RFC3339Nano rather than the fixed tsLayout because the modernc
// SQLite driver normalizes values read from TIMESTAMP-typed columns to RFC3339
// (omitting zero fractional seconds). RFC3339Nano accepts both that normalized
// form and our fixed-width stored form.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// Store is the live database handle used by the running application.
type Store struct {
	db *sql.DB
}

// openDB opens (creating if needed) the SQLite database at path and applies the
// standard PRAGMAs. It does not run migrations.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}

	// wha is the sole writer; a single connection avoids lock contention.
	db.SetMaxOpenConns(1)

	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply %q: %w", p, err)
		}
	}

	return db, nil
}

// Open opens (creating if needed) the SQLite database at path, applies PRAGMAs,
// and runs all pending migrations. migrate.ErrNoChange is treated as success.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}

	if err := migrateUp(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}
	return nil
}

// Checkpoint runs a WAL truncating checkpoint, releasing freed pages back to
// the OS. Call this after a prune pass to reclaim disk space.
func (s *Store) Checkpoint(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`)
	if err != nil {
		return fmt.Errorf("wal checkpoint: %w", err)
	}
	return nil
}
