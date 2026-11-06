package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"uptime-monitor/internal/monitor"
)

func TestDeleteMonitorRemovesItsHistory(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	m := mustCreateMonitor(t, s, "gone")
	if _, _, err := s.RecordCheck(ctx, failedCheck(m.ID, time.Now().UTC(), "down")); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMonitor(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListChecks(ctx, m.ID, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("list after delete = %v, want ErrNotFound", err)
	}
	if err := s.DeleteMonitor(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestListChecksReturnsTheNewestWindowOldestFirst(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	m := mustCreateMonitor(t, s, "history")
	base := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	for i, code := range []int{200, 500, 204} {
		if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
			MonitorID:  m.ID,
			StatusCode: code,
			LatencyMs:  int64(i + 1),
			Success:    code < 400,
			Error:      "",
			CheckedAt:  base.Add(time.Duration(i) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.ListChecks(ctx, m.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].StatusCode != 500 || got[1].StatusCode != 204 {
		t.Fatalf("checks = %+v", got)
	}
	if got[0].ID == 0 || got[1].ID == 0 {
		t.Fatalf("ids = %d, %d", got[0].ID, got[1].ID)
	}
}

func TestListIncidentsByStatus(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	openMon := mustCreateMonitor(t, s, "open-monitor")
	closedMon := mustCreateMonitor(t, s, "closed-monitor")
	for _, id := range []int64{openMon.ID, closedMon.ID} {
		for range 3 {
			clock = clock.Add(time.Second)
			if _, _, err := s.RecordCheck(ctx, failedCheck(id, clock, "down")); err != nil {
				t.Fatal(err)
			}
		}
	}
	closed := listIncidents(t, s, closedMon.ID)
	if _, err := s.ResolveIncident(ctx, closed[0].ID); err != nil {
		t.Fatal(err)
	}

	openRows, err := s.ListIncidentsByStatus(ctx, monitor.IncidentOpen)
	if err != nil {
		t.Fatal(err)
	}
	if len(openRows) != 1 || openRows[0].MonitorID != openMon.ID || openRows[0].MonitorName != "open-monitor" {
		t.Fatalf("open = %+v", openRows)
	}
	resolved, err := s.ListIncidentsByStatus(ctx, monitor.IncidentResolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].MonitorName != "closed-monitor" || resolved[0].ResolvedAt == nil {
		t.Fatalf("resolved = %+v", resolved)
	}
	all, err := s.ListIncidentsByStatus(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("all = %d, want 2", len(all))
	}
}

func TestPruneChecksKeepsTheLast24Hours(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	m := mustCreateMonitor(t, s, "old")
	now := time.Now().UTC()
	old := now.Add(-25 * time.Hour)
	if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
		MonitorID: m.ID, StatusCode: 500, Success: false, Error: "old", CheckedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
		MonitorID: m.ID, StatusCode: 200, Success: true, CheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.PruneChecks(ctx, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned = %d, want 1", n)
	}
	checks, err := s.ListChecks(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || !checks[0].Success {
		t.Fatalf("remaining = %+v", checks)
	}
}
