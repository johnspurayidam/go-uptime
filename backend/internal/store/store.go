package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const timeLayout = "2006-01-02T15:04:05.000000000Z"

// UptimeWindow is how long a check counts toward the 24 hour uptime percentage.
// PruneChecks deletes probes older than this.
const UptimeWindow = 24 * time.Hour

var ErrNotFound = errors.New("not found")

var ErrNotOpen = errors.New("incident is not open")

var ErrNotActive = errors.New("incident is not active")

type Store struct {
	db  *sql.DB
	now func() time.Time
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Open opens the SQLite file at path, creates the tables if they are missing, and returns a Store.
// One connection is used so the foreign key pragma applies to every query. The caller must Close it.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}

	s := &Store{
		db:  db,
		now: func() time.Time { return time.Now().UTC() },
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database connection.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close db: %w", err)
	}
	return nil
}

func dsn(path string) string {
	return "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
}

func (s *Store) migrate() error {
	for _, stmt := range schema {
		if _, err := s.db.Exec(stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

var schema = []string{
	`CREATE TABLE IF NOT EXISTS monitors (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		url TEXT NOT NULL,
		interval_seconds INTEGER NOT NULL,
		created_at TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS checks (
		id INTEGER PRIMARY KEY,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		status_code INTEGER NOT NULL,
		latency_ms INTEGER NOT NULL,
		success INTEGER NOT NULL CHECK (success IN (0, 1)),
		error TEXT NOT NULL DEFAULT '',
		checked_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_checks_monitor_checked_at
		ON checks (monitor_id, checked_at)`,
	`CREATE TABLE IF NOT EXISTS incidents (
		id INTEGER PRIMARY KEY,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		status TEXT NOT NULL,
		opened_at TEXT NOT NULL,
		acked_at TEXT,
		resolved_at TEXT,
		last_error TEXT NOT NULL DEFAULT ''
	)`,
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func parseTime(value string) (time.Time, error) {
	t, err := time.Parse(timeLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse time %q: %w", value, err)
	}
	return t, nil
}

func parseNullTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	t, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
