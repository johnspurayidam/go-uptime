package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"uptime-monitor/internal/monitor"
)

// CreateMonitor inserts one monitor and returns it with the id assigned by SQLite.
func (s *Store) CreateMonitor(ctx context.Context, m monitor.Monitor) (monitor.Monitor, error) {
	if m.CreatedAt.IsZero() {
		m.CreatedAt = s.now()
	}
	if m.IntervalSeconds <= 0 {
		m.IntervalSeconds = 30
	}
	return insertMonitor(ctx, s.db, m)
}

func insertMonitor(ctx context.Context, e execer, m monitor.Monitor) (monitor.Monitor, error) {
	res, err := e.ExecContext(ctx,
		`INSERT INTO monitors (name, url, interval_seconds, created_at) VALUES (?, ?, ?, ?)`,
		m.Name, m.URL, m.IntervalSeconds, formatTime(m.CreatedAt))
	if err != nil {
		return monitor.Monitor{}, fmt.Errorf("insert monitor %s: %w", m.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return monitor.Monitor{}, fmt.Errorf("monitor id: %w", err)
	}
	m.ID = id
	return m, nil
}

// DeleteMonitor removes a monitor. Its checks and incidents are removed by the foreign key.
// A missing id returns ErrNotFound.
func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete monitor %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete monitor %d rows: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("monitor %d: %w", id, ErrNotFound)
	}
	return nil
}

// PruneChecks deletes probes checked before the cutoff and returns how many rows were removed.
// Incidents are kept. The uptime percentage only looks at the last 24 hours, which is the same window.
func (s *Store) PruneChecks(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM checks WHERE checked_at < ?`, formatTime(before))
	if err != nil {
		return 0, fmt.Errorf("prune checks: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune checks rows: %w", err)
	}
	return n, nil
}

// ListChecks returns up to limit probes for one monitor, oldest of that window first.
// The query reads the newest rows and reverses them so a chart can plot left to right.
// An unknown monitor returns ErrNotFound rather than an empty list.
func (s *Store) ListChecks(ctx context.Context, monitorID int64, limit int) ([]monitor.CheckResult, error) {
	if limit < 1 {
		limit = 100
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors WHERE id = ?`, monitorID).Scan(&n)
	if err != nil {
		return nil, fmt.Errorf("lookup monitor %d: %w", monitorID, err)
	}
	if n == 0 {
		return nil, fmt.Errorf("monitor %d: %w", monitorID, ErrNotFound)
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT id, monitor_id, status_code, latency_ms, success, error, checked_at
		FROM checks
		WHERE monitor_id = ?
		ORDER BY checked_at DESC, id DESC
		LIMIT ?`, monitorID, limit)
	if err != nil {
		return nil, fmt.Errorf("list checks for monitor %d: %w", monitorID, err)
	}
	defer rows.Close()

	out := make([]monitor.CheckResult, 0)
	for rows.Next() {
		var (
			result  monitor.CheckResult
			success int
			checked string
		)
		if err := rows.Scan(&result.ID, &result.MonitorID, &result.StatusCode, &result.LatencyMs, &success, &result.Error, &checked); err != nil {
			return nil, fmt.Errorf("scan check: %w", err)
		}
		result.Success = success == 1
		result.CheckedAt, err = parseTime(checked)
		if err != nil {
			return nil, err
		}
		out = append(out, result)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checks: %w", err)
	}
	slices.Reverse(out)
	return out, nil
}

// SeedMonitors inserts seeds only when the monitors table is empty.
// The returned count is how many rows this call inserted, which is zero on later startups.
func (s *Store) SeedMonitors(ctx context.Context, seeds []monitor.Monitor) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin seed: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM monitors`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count monitors: %w", err)
	}
	if n > 0 {
		return 0, nil
	}

	for _, m := range seeds {
		if m.CreatedAt.IsZero() {
			m.CreatedAt = s.now()
		}
		if m.IntervalSeconds <= 0 {
			m.IntervalSeconds = 30
		}
		if _, err := insertMonitor(ctx, tx, m); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit seed: %w", err)
	}
	return len(seeds), nil
}

// ListMonitors returns every monitor, oldest id first. The checker polls this list.
func (s *Store) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, url, interval_seconds, created_at FROM monitors ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()

	monitors := make([]monitor.Monitor, 0)
	for rows.Next() {
		var (
			m       monitor.Monitor
			created string
		)
		if err := rows.Scan(&m.ID, &m.Name, &m.URL, &m.IntervalSeconds, &created); err != nil {
			return nil, fmt.Errorf("scan monitor: %w", err)
		}
		m.CreatedAt, err = parseTime(created)
		if err != nil {
			return nil, err
		}
		monitors = append(monitors, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate monitors: %w", err)
	}
	return monitors, nil
}

// ListMonitorSummaries returns every monitor with its current status, last latency,
// and uptime over the last 24 hours. It is the body of GET /api/monitors.
func (s *Store) ListMonitorSummaries(ctx context.Context) ([]monitor.MonitorSummary, error) {
	monitors, err := s.ListMonitors(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]monitor.MonitorSummary, 0, len(monitors))
	for _, m := range monitors {
		summary, err := s.summarize(ctx, m)
		if err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, nil
}

// summarize adds status, latency, and uptime onto one monitor.
// Status comes from the newest check of any age. Uptime counts only the last 24 hours,
// and is nil when that window has no checks.
func (s *Store) summarize(ctx context.Context, m monitor.Monitor) (monitor.MonitorSummary, error) {
	summary := monitor.MonitorSummary{
		Monitor: m,
		Status:  monitor.StatusUnknown,
	}

	var (
		latency int64
		success int
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT latency_ms, success FROM checks
		WHERE monitor_id = ?
		ORDER BY checked_at DESC, id DESC
		LIMIT 1`, m.ID).Scan(&latency, &success)
	if errors.Is(err, sql.ErrNoRows) {
		return summary, nil
	}
	if err != nil {
		return monitor.MonitorSummary{}, fmt.Errorf("latest check for monitor %d: %w", m.ID, err)
	}

	summary.LastLatencyMs = &latency
	summary.Status = monitor.StatusDown
	if success == 1 {
		summary.Status = monitor.StatusUp
	}

	pct, err := s.uptimePercent(ctx, m.ID)
	if err != nil {
		return monitor.MonitorSummary{}, err
	}
	summary.UptimePercent = pct
	return summary, nil
}

func (s *Store) uptimePercent(ctx context.Context, monitorID int64) (*float64, error) {
	since := s.now().Add(-UptimeWindow)
	var total, ok int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(success), 0)
		FROM checks
		WHERE monitor_id = ? AND checked_at >= ?`,
		monitorID, formatTime(since)).Scan(&total, &ok)
	if err != nil {
		return nil, fmt.Errorf("uptime for monitor %d: %w", monitorID, err)
	}
	if total == 0 {
		return nil, nil
	}
	pct := math.Round((float64(ok)/float64(total))*1000) / 10
	return &pct, nil
}
