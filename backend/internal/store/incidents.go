package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"uptime-monitor/internal/monitor"
)

// RecordCheck stores one probe and applies the incident rules in the same transaction.
// The returned check includes the id assigned by SQLite.
// Decision says whether an incident was opened, resolved, updated, or left alone.
// A failed probe is still saved. A non-nil error means the write itself failed.
func (s *Store) RecordCheck(ctx context.Context, result monitor.CheckResult) (monitor.CheckResult, monitor.Decision, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, fmt.Errorf("begin record check: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO checks (monitor_id, status_code, latency_ms, success, error, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		result.MonitorID, result.StatusCode, result.LatencyMs, boolInt(result.Success), result.Error, formatTime(result.CheckedAt),
	)
	if err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, fmt.Errorf("insert check: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, fmt.Errorf("check id: %w", err)
	}
	result.ID = id

	active, err := activeIncident(ctx, tx, result.MonitorID)
	if err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, err
	}

	failures := 0
	if !result.Success && !active.IsActive() {
		failures, err = trailingFailures(ctx, tx, result.MonitorID)
		if err != nil {
			return monitor.CheckResult{}, monitor.DecisionNone, err
		}
	}

	decision := monitor.DecideIncident(result.Success, failures, active)
	switch decision {
	case monitor.DecisionOpen:
		_, err = tx.ExecContext(ctx, `
			INSERT INTO incidents (monitor_id, status, opened_at, last_error)
			VALUES (?, ?, ?, ?)`,
			result.MonitorID, monitor.IncidentOpen, formatTime(result.CheckedAt), result.Error)
	case monitor.DecisionResolve:
		_, err = tx.ExecContext(ctx, `
			UPDATE incidents SET status = ?, resolved_at = ? WHERE id = ?`,
			monitor.IncidentResolved, formatTime(result.CheckedAt), active.ID)
	case monitor.DecisionUpdate:
		_, err = tx.ExecContext(ctx, `
			UPDATE incidents SET last_error = ? WHERE id = ?`,
			result.Error, active.ID)
	}
	if err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, fmt.Errorf("apply incident decision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return monitor.CheckResult{}, monitor.DecisionNone, fmt.Errorf("commit record check: %w", err)
	}
	return result, decision, nil
}

// AckIncident marks an open incident as acknowledged and returns the updated row, including the monitor name.
// Acking an incident that is not open returns ErrNotOpen or ErrNotFound.
func (s *Store) AckIncident(ctx context.Context, id int64) (monitor.Incident, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE incidents SET status = ?, acked_at = ? WHERE id = ? AND status = ?`,
		monitor.IncidentAcked, formatTime(s.now()), id, monitor.IncidentOpen)
	if err != nil {
		return monitor.Incident{}, fmt.Errorf("ack incident %d: %w", id, err)
	}
	if err := s.transitionError(ctx, res, id, ErrNotOpen); err != nil {
		return monitor.Incident{}, err
	}
	return s.incidentWithName(ctx, id)
}

// ResolveIncident closes an open or acknowledged incident and returns the updated row.
// This is the manual path. A later successful check resolves an incident on its own inside RecordCheck.
func (s *Store) ResolveIncident(ctx context.Context, id int64) (monitor.Incident, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE incidents SET status = ?, resolved_at = ?
		WHERE id = ? AND status IN (?, ?)`,
		monitor.IncidentResolved, formatTime(s.now()), id, monitor.IncidentOpen, monitor.IncidentAcked)
	if err != nil {
		return monitor.Incident{}, fmt.Errorf("resolve incident %d: %w", id, err)
	}
	if err := s.transitionError(ctx, res, id, ErrNotActive); err != nil {
		return monitor.Incident{}, err
	}
	return s.incidentWithName(ctx, id)
}

// ListIncidentsByStatus returns incidents for the API, newest opened_at first.
// An empty status returns every incident. Each row includes the monitor name.
func (s *Store) ListIncidentsByStatus(ctx context.Context, status string) ([]monitor.Incident, error) {
	query := `
		SELECT i.id, i.monitor_id, i.status, i.opened_at, i.acked_at, i.resolved_at, i.last_error, m.name
		FROM incidents i
		JOIN monitors m ON m.id = i.monitor_id`
	var args []any
	if status != "" {
		query += ` WHERE i.status = ?`
		args = append(args, status)
	}
	query += ` ORDER BY i.opened_at DESC, i.id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	out := make([]monitor.Incident, 0)
	for rows.Next() {
		inc, err := scanNamedIncident(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return out, nil
}

func (s *Store) transitionError(ctx context.Context, res sql.Result, id int64, wrongStatus error) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected for incident %d: %w", id, err)
	}
	if n > 0 {
		return nil
	}
	if _, err := s.incidentByID(ctx, id); err != nil {
		return err
	}
	return fmt.Errorf("incident %d: %w", id, wrongStatus)
}

func (s *Store) ListIncidents(ctx context.Context, monitorID int64) ([]monitor.Incident, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+incidentColumns+`
		FROM incidents WHERE monitor_id = ? ORDER BY id`, monitorID)
	if err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	defer rows.Close()

	out := make([]monitor.Incident, 0)
	for rows.Next() {
		inc, err := scanIncident(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, inc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate incidents: %w", err)
	}
	return out, nil
}

func (s *Store) incidentWithName(ctx context.Context, id int64) (monitor.Incident, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT i.id, i.monitor_id, i.status, i.opened_at, i.acked_at, i.resolved_at, i.last_error, m.name
		FROM incidents i
		JOIN monitors m ON m.id = i.monitor_id
		WHERE i.id = ?`, id)
	inc, err := scanNamedIncident(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return monitor.Incident{}, fmt.Errorf("incident %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return monitor.Incident{}, fmt.Errorf("get incident %d: %w", id, err)
	}
	return inc, nil
}

func (s *Store) incidentByID(ctx context.Context, id int64) (monitor.Incident, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+incidentColumns+` FROM incidents WHERE id = ?`, id)
	inc, err := scanIncident(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return monitor.Incident{}, fmt.Errorf("incident %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return monitor.Incident{}, fmt.Errorf("get incident %d: %w", id, err)
	}
	return inc, nil
}

const incidentColumns = `id, monitor_id, status, opened_at, acked_at, resolved_at, last_error`

type rowScanner func(dest ...any) error

func scanNamedIncident(scan rowScanner) (monitor.Incident, error) {
	inc, name, err := scanIncidentFields(scan, true)
	if err != nil {
		return monitor.Incident{}, err
	}
	inc.MonitorName = name
	return inc, nil
}

func scanIncident(scan rowScanner) (monitor.Incident, error) {
	inc, _, err := scanIncidentFields(scan, false)
	return inc, err
}

func scanIncidentFields(scan rowScanner, withName bool) (monitor.Incident, string, error) {
	var (
		inc                 monitor.Incident
		opened              string
		ackedAt, resolvedAt sql.NullString
		name                string
	)
	dest := []any{&inc.ID, &inc.MonitorID, &inc.Status, &opened, &ackedAt, &resolvedAt, &inc.LastError}
	if withName {
		dest = append(dest, &name)
	}
	if err := scan(dest...); err != nil {
		return monitor.Incident{}, "", err
	}
	var err error
	inc.OpenedAt, err = parseTime(opened)
	if err != nil {
		return monitor.Incident{}, "", err
	}
	inc.AckedAt, err = parseNullTime(ackedAt)
	if err != nil {
		return monitor.Incident{}, "", err
	}
	inc.ResolvedAt, err = parseNullTime(resolvedAt)
	if err != nil {
		return monitor.Incident{}, "", err
	}
	return inc, name, nil
}

func activeIncident(ctx context.Context, tx *sql.Tx, monitorID int64) (*monitor.Incident, error) {
	row := tx.QueryRowContext(ctx, `
		SELECT `+incidentColumns+`
		FROM incidents
		WHERE monitor_id = ? AND status IN (?, ?)
		ORDER BY id DESC
		LIMIT 1`, monitorID, monitor.IncidentOpen, monitor.IncidentAcked)
	inc, err := scanIncident(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("active incident for monitor %d: %w", monitorID, err)
	}
	return &inc, nil
}

// trailingFailures counts failed checks at the end of the history, stopping at the third.
// Checks at or before the latest resolved_at are ignored, so closing an incident starts the next streak over.
func trailingFailures(ctx context.Context, tx *sql.Tx, monitorID int64) (int, error) {
	var resolved sql.NullString
	err := tx.QueryRowContext(ctx, `
		SELECT resolved_at FROM incidents
		WHERE monitor_id = ? AND status = ? AND resolved_at IS NOT NULL
		ORDER BY resolved_at DESC
		LIMIT 1`, monitorID, monitor.IncidentResolved).Scan(&resolved)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("latest resolved incident: %w", err)
	}

	cutoff := ""
	if resolved.Valid {
		cutoff = resolved.String
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT success FROM checks
		WHERE monitor_id = ? AND checked_at > ?
		ORDER BY checked_at DESC, id DESC
		LIMIT ?`, monitorID, cutoff, monitor.FailureThreshold)
	if err != nil {
		return 0, fmt.Errorf("recent checks: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var success int
		if err := rows.Scan(&success); err != nil {
			return 0, fmt.Errorf("scan recent check: %w", err)
		}
		if success == 1 {
			break
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate recent checks: %w", err)
	}
	return count, nil
}
