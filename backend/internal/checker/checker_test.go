package checker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"uptime-monitor/internal/monitor"
)

type sliceSource []monitor.Monitor

func (s sliceSource) ListMonitors(context.Context) ([]monitor.Monitor, error) {
	return s, nil
}

func TestCheckStatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantOK     bool
	}{
		{name: "2xx is up", statusCode: http.StatusOK, wantOK: true},
		{name: "3xx is up", statusCode: http.StatusMovedPermanently, wantOK: true},
		{name: "4xx is down", statusCode: http.StatusNotFound, wantOK: false},
		{name: "5xx is down", statusCode: http.StatusInternalServerError, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
			}))
			t.Cleanup(srv.Close)

			got := New(discardLogger(), 1, nil).check(monitor.Monitor{
				ID:  1,
				URL: srv.URL,
			})

			if got.Success != tt.wantOK {
				t.Fatalf("success = %v, want %v (status %d, err %q)", got.Success, tt.wantOK, got.StatusCode, got.Error)
			}
			if got.StatusCode != tt.statusCode {
				t.Fatalf("status = %d, want %d", got.StatusCode, tt.statusCode)
			}
			if got.CheckedAt.IsZero() {
				t.Fatal("checked_at is zero")
			}
			if tt.wantOK && got.Error != "" {
				t.Fatalf("error = %q, want empty", got.Error)
			}
			if !tt.wantOK && got.Error == "" {
				t.Fatal("expected an error message")
			}
		})
	}
}

func TestCheckDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(downstream.Close)

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, downstream.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	got := New(discardLogger(), 1, nil).check(monitor.Monitor{ID: 1, URL: redirect.URL})
	if !got.Success {
		t.Fatalf("success = false, want true for 302 (err %q)", got.Error)
	}
	if got.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", got.StatusCode)
	}
}

func TestCheckConnectionError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close()

	got := New(discardLogger(), 1, nil).check(monitor.Monitor{ID: 1, URL: url})
	if got.Success {
		t.Fatal("success = true, want false")
	}
	if got.StatusCode != 0 {
		t.Fatalf("status = %d, want 0", got.StatusCode)
	}
	if got.Error == "" {
		t.Fatal("expected an error message")
	}
}

func TestCheckTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	c := New(discardLogger(), 1, nil)
	c.timeout = 30 * time.Millisecond

	got := c.check(monitor.Monitor{ID: 1, URL: srv.URL})
	if got.Success {
		t.Fatal("success = true, want false")
	}
	if got.StatusCode != 0 {
		t.Fatalf("status = %d, want 0", got.StatusCode)
	}
	if !strings.Contains(got.Error, "deadline exceeded") {
		t.Fatalf("error = %q, want the context deadline", got.Error)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	results := make(chan monitor.CheckResult, 4)
	c := New(discardLogger(), 2, func(result monitor.CheckResult) {
		results <- result
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx, sliceSource{{
			ID:              7,
			Name:            "local",
			URL:             srv.URL,
			IntervalSeconds: 3600,
		}})
		close(done)
	}()

	select {
	case got := <-results:
		if !got.Success || got.MonitorID != 7 || got.StatusCode != http.StatusNoContent {
			t.Fatalf("result = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a check")
	}

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("checker did not stop after cancel")
	}
}

type memSource struct {
	mu       sync.Mutex
	monitors []monitor.Monitor
}

func (m *memSource) ListMonitors(context.Context) ([]monitor.Monitor, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]monitor.Monitor, len(m.monitors))
	copy(out, m.monitors)
	return out, nil
}

func (m *memSource) set(monitors []monitor.Monitor) {
	m.mu.Lock()
	m.monitors = monitors
	m.mu.Unlock()
}

func TestRunChecksMonitorAddedLater(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	src := &memSource{}
	results := make(chan monitor.CheckResult, 4)
	c := New(discardLogger(), 2, func(result monitor.CheckResult) {
		results <- result
	})
	c.pollInterval = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx, src)
		close(done)
	}()

	src.set([]monitor.Monitor{{
		ID:              3,
		Name:            "added",
		URL:             srv.URL,
		IntervalSeconds: 3600,
	}})

	select {
	case got := <-results:
		if !got.Success || got.MonitorID != 3 {
			t.Fatalf("result = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the added monitor")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("checker did not stop after cancel")
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
