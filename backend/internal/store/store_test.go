package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"uptime-monitor/internal/monitor"
)

func TestIncidentFlow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		steps            []string
		wantCount        int
		wantStatus       string
		wantLastError    string
		wantAcked        bool
		wantLastDecision monitor.Decision
	}{
		{
			name:             "two failures do not open",
			steps:            []string{"fail", "fail"},
			wantCount:        0,
			wantLastDecision: monitor.DecisionNone,
		},
		{
			name:             "the third failure opens",
			steps:            []string{"fail", "fail", "fail"},
			wantCount:        1,
			wantStatus:       monitor.IncidentOpen,
			wantLastError:    "fail-3",
			wantLastDecision: monitor.DecisionOpen,
		},
		{
			name:             "a fourth failure does not open a second incident",
			steps:            []string{"fail", "fail", "fail", "fail"},
			wantCount:        1,
			wantStatus:       monitor.IncidentOpen,
			wantLastError:    "fail-4",
			wantLastDecision: monitor.DecisionUpdate,
		},
		{
			name:             "success resolves an open incident",
			steps:            []string{"fail", "fail", "fail", "ok"},
			wantCount:        1,
			wantStatus:       monitor.IncidentResolved,
			wantLastDecision: monitor.DecisionResolve,
		},
		{
			name:             "a failure while acknowledged does not open another",
			steps:            []string{"fail", "fail", "fail", "ack", "fail"},
			wantCount:        1,
			wantStatus:       monitor.IncidentAcked,
			wantLastError:    "fail-5",
			wantAcked:        true,
			wantLastDecision: monitor.DecisionUpdate,
		},
		{
			name:             "success resolves an acknowledged incident",
			steps:            []string{"fail", "fail", "fail", "ack", "ok"},
			wantCount:        1,
			wantStatus:       monitor.IncidentResolved,
			wantAcked:        true,
			wantLastDecision: monitor.DecisionResolve,
		},
		{
			name:             "a manual resolve starts the failure streak over",
			steps:            []string{"fail", "fail", "fail", "resolve", "fail", "fail"},
			wantCount:        1,
			wantStatus:       monitor.IncidentResolved,
			wantLastDecision: monitor.DecisionNone,
		},
		{
			name:             "three new failures after a resolve open a second incident",
			steps:            []string{"fail", "fail", "fail", "resolve", "fail", "fail", "fail"},
			wantCount:        2,
			wantStatus:       monitor.IncidentOpen,
			wantLastError:    "fail-7",
			wantLastDecision: monitor.DecisionOpen,
		},
		{
			name:             "a success breaks the streak",
			steps:            []string{"fail", "fail", "ok", "fail", "fail"},
			wantCount:        0,
			wantLastDecision: monitor.DecisionNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := openTestStore(t)
			clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
			s.now = func() time.Time { return clock }

			m := mustCreateMonitor(t, s, tt.name)
			ctx := context.Background()
			var last monitor.Decision

			for i, kind := range tt.steps {
				clock = clock.Add(time.Second)
				switch kind {
				case "ack":
					mustAckLatest(t, s, m.ID)
				case "resolve":
					mustResolveLatest(t, s, m.ID)
				case "fail", "ok":
					var err error
					_, last, err = s.RecordCheck(ctx, monitor.CheckResult{
						MonitorID:  m.ID,
						StatusCode: statusFor(kind),
						LatencyMs:  15,
						Success:    kind == "ok",
						Error:      fmt.Sprintf("%s-%d", kind, i+1),
						CheckedAt:  clock,
					})
					if err != nil {
						t.Fatalf("record %s: %v", kind, err)
					}
				default:
					t.Fatalf("unknown step %q", kind)
				}
			}

			if last != tt.wantLastDecision {
				t.Fatalf("last decision = %q, want %q", last, tt.wantLastDecision)
			}
			got := listIncidents(t, s, m.ID)
			if len(got) != tt.wantCount {
				t.Fatalf("incidents = %d, want %d (%+v)", len(got), tt.wantCount, got)
			}
			if tt.wantCount == 0 {
				return
			}
			latest := got[len(got)-1]
			if latest.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", latest.Status, tt.wantStatus)
			}
			if tt.wantLastError != "" && latest.LastError != tt.wantLastError {
				t.Fatalf("last_error = %q, want %q", latest.LastError, tt.wantLastError)
			}
			if tt.wantAcked && latest.AckedAt == nil {
				t.Fatal("acked_at is nil")
			}
			if !tt.wantAcked && latest.AckedAt != nil {
				t.Fatalf("acked_at = %s, want nil", latest.AckedAt)
			}
			if tt.wantStatus == monitor.IncidentResolved && latest.ResolvedAt == nil {
				t.Fatal("resolved_at is nil")
			}
			if tt.wantStatus != monitor.IncidentResolved && latest.ResolvedAt != nil {
				t.Fatalf("resolved_at = %s, want nil", latest.ResolvedAt)
			}
		})
	}
}

func TestIncidentsArePerMonitor(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	clock := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }
	ctx := context.Background()

	down := mustCreateMonitor(t, s, "down")
	other := mustCreateMonitor(t, s, "other")
	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Second)
		if _, _, err := s.RecordCheck(ctx, failedCheck(down.ID, clock, "down")); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.RecordCheck(ctx, failedCheck(other.ID, clock.Add(time.Second), "other")); err != nil {
		t.Fatal(err)
	}
	if got := listIncidents(t, s, down.ID); len(got) != 1 || got[0].Status != monitor.IncidentOpen {
		t.Fatalf("down incidents = %+v", got)
	}
	if got := listIncidents(t, s, other.ID); len(got) != 0 {
		t.Fatalf("other incidents = %+v", got)
	}
}

func TestAckAndResolveErrors(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.AckIncident(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ack missing = %v, want ErrNotFound", err)
	}
	if _, err := s.ResolveIncident(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve missing = %v, want ErrNotFound", err)
	}

	m := mustCreateMonitor(t, s, "errors")
	clock := time.Now().UTC()
	for i := 0; i < 3; i++ {
		clock = clock.Add(time.Second)
		if _, _, err := s.RecordCheck(ctx, failedCheck(m.ID, clock, "boom")); err != nil {
			t.Fatal(err)
		}
	}
	inc := listIncidents(t, s, m.ID)[0]
	if _, err := s.AckIncident(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AckIncident(ctx, inc.ID); !errors.Is(err, ErrNotOpen) {
		t.Fatalf("second ack = %v, want ErrNotOpen", err)
	}
	if _, err := s.ResolveIncident(ctx, inc.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveIncident(ctx, inc.ID); !errors.Is(err, ErrNotActive) {
		t.Fatalf("second resolve = %v, want ErrNotActive", err)
	}
}

func TestSummaryUptime(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	m := mustCreateMonitor(t, s, "summary")

	got := mustSummary(t, s, m.ID)
	if got.Status != monitor.StatusUnknown || got.LastLatencyMs != nil || got.UptimePercent != nil {
		t.Fatalf("empty summary = %+v", got)
	}

	old := time.Now().UTC().Add(-25 * time.Hour)
	if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
		MonitorID: m.ID, StatusCode: 200, LatencyMs: 111, Success: true, CheckedAt: old,
	}); err != nil {
		t.Fatal(err)
	}
	got = mustSummary(t, s, m.ID)
	if got.Status != monitor.StatusUp || got.LastLatencyMs == nil || *got.LastLatencyMs != 111 || got.UptimePercent != nil {
		t.Fatalf("old sample summary = %+v", got)
	}

	now := time.Now().UTC()
	if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
		MonitorID: m.ID, StatusCode: 200, LatencyMs: 20, Success: true, CheckedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.RecordCheck(ctx, monitor.CheckResult{
		MonitorID: m.ID, StatusCode: 500, LatencyMs: 40, Success: false, Error: "500", CheckedAt: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	got = mustSummary(t, s, m.ID)
	if got.Status != monitor.StatusDown || got.LastLatencyMs == nil || *got.LastLatencyMs != 40 {
		t.Fatalf("latest summary = %+v", got)
	}
	if got.UptimePercent == nil || *got.UptimePercent != 50 {
		t.Fatalf("uptime = %v, want 50", got.UptimePercent)
	}
}

func TestSeedMonitorsOnce(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := context.Background()
	seeds := []monitor.Monitor{
		{Name: "Example", URL: "https://example.com", IntervalSeconds: 30},
		{Name: "Always failing", URL: "https://httpstat.us/500", IntervalSeconds: 30},
	}

	n, err := s.SeedMonitors(ctx, seeds)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("inserted = %d, want 2", n)
	}
	n, err = s.SeedMonitors(ctx, seeds)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("second insert = %d, want 0", n)
	}
	got, err := s.ListMonitors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "Example" || got[1].URL != "https://httpstat.us/500" {
		t.Fatalf("monitors = %+v", got)
	}
	if got[0].ID == 0 || got[1].ID == 0 || got[0].ID == got[1].ID {
		t.Fatalf("ids = %d, %d", got[0].ID, got[1].ID)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	_, err := s.db.Exec(`
		INSERT INTO checks (monitor_id, status_code, latency_ms, success, error, checked_at)
		VALUES (999, 0, 0, 0, '', '2026-11-06T12:00:00.000000000Z')`)
	if err == nil {
		t.Fatal("insert with a missing monitor succeeded")
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return s
}

func mustCreateMonitor(t *testing.T, s *Store, name string) monitor.Monitor {
	t.Helper()
	m, err := s.CreateMonitor(context.Background(), monitor.Monitor{
		Name:            name,
		URL:             "https://example.com/" + name,
		IntervalSeconds: 30,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func mustAckLatest(t *testing.T, s *Store, monitorID int64) {
	t.Helper()
	incs := listIncidents(t, s, monitorID)
	if len(incs) == 0 {
		t.Fatal("ack with no incidents")
	}
	if _, err := s.AckIncident(context.Background(), incs[len(incs)-1].ID); err != nil {
		t.Fatal(err)
	}
}

func mustResolveLatest(t *testing.T, s *Store, monitorID int64) {
	t.Helper()
	incs := listIncidents(t, s, monitorID)
	if len(incs) == 0 {
		t.Fatal("resolve with no incidents")
	}
	if _, err := s.ResolveIncident(context.Background(), incs[len(incs)-1].ID); err != nil {
		t.Fatal(err)
	}
}

func listIncidents(t *testing.T, s *Store, monitorID int64) []monitor.Incident {
	t.Helper()
	got, err := s.ListIncidents(context.Background(), monitorID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func mustSummary(t *testing.T, s *Store, id int64) monitor.MonitorSummary {
	t.Helper()
	all, err := s.ListMonitorSummaries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range all {
		if summary.ID == id {
			return summary
		}
	}
	t.Fatalf("monitor %d not in %+v", id, all)
	return monitor.MonitorSummary{}
}

func failedCheck(monitorID int64, at time.Time, message string) monitor.CheckResult {
	return monitor.CheckResult{
		MonitorID:  monitorID,
		StatusCode: 500,
		LatencyMs:  5,
		Success:    false,
		Error:      message,
		CheckedAt:  at,
	}
}

func statusFor(kind string) int {
	if kind == "ok" {
		return 200
	}
	return 500
}
