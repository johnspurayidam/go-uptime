package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"uptime-monitor/internal/events"
	"uptime-monitor/internal/monitor"
	"uptime-monitor/internal/store"
)

func TestCreateMonitorValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "missing name", body: `{"url":"https://example.com"}`, wantErr: "name is required"},
		{name: "blank name", body: `{"name":"  ","url":"https://example.com"}`, wantErr: "name is required"},
		{name: "missing url", body: `{"name":"Example"}`, wantErr: "url is required"},
		{name: "relative url", body: `{"name":"Example","url":"/health"}`, wantErr: "url must be an absolute http or https URL"},
		{name: "non-http scheme", body: `{"name":"Example","url":"ftp://example.com/file"}`, wantErr: "url must be an absolute http or https URL"},
		{name: "interval too small", body: `{"name":"Example","url":"https://example.com","interval_seconds":1}`, wantErr: "interval_seconds must be between 5 and 86400"},
		{name: "explicit zero interval", body: `{"name":"Example","url":"https://example.com","interval_seconds":0}`, wantErr: "interval_seconds must be between 5 and 86400"},
		{name: "unknown field", body: `{"name":"Example","url":"https://example.com","extra":true}`, wantErr: "unknown field"},
		{name: "empty body", body: ``, wantErr: "request body is required"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := call(t, http.MethodPost, "/api/monitors", tt.body, fakeStore{})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantErr) {
				t.Fatalf("body = %s, want substring %q", rec.Body.String(), tt.wantErr)
			}
		})
	}
}

func TestCreateMonitorDefaultsInterval(t *testing.T) {
	t.Parallel()
	var got monitor.Monitor
	db := fakeStore{
		create: func(_ context.Context, m monitor.Monitor) (monitor.Monitor, error) {
			got = m
			m.ID = 9
			return m, nil
		},
	}
	rec := call(t, http.MethodPost, "/api/monitors", `{"name":" Example ","url":" https://example.com "}`, db)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	if got.Name != "Example" || got.URL != "https://example.com" || got.IntervalSeconds != 30 {
		t.Fatalf("stored = %+v", got)
	}
	var summary monitor.MonitorSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &summary); err != nil {
		t.Fatal(err)
	}
	if summary.ID != 9 || summary.Status != monitor.StatusUnknown || summary.LastLatencyMs != nil {
		t.Fatalf("response = %+v", summary)
	}
}

func TestDeleteAndMiss(t *testing.T) {
	t.Parallel()
	var deleted int64
	db := fakeStore{
		deleteMonitor: func(_ context.Context, id int64) error {
			if id != 4 {
				return store.ErrNotFound
			}
			deleted = id
			return nil
		},
	}
	ok := call(t, http.MethodDelete, "/api/monitors/4", "", db)
	if ok.Code != http.StatusNoContent || ok.Body.Len() != 0 {
		t.Fatalf("delete status = %d, body %q", ok.Code, ok.Body.String())
	}
	if deleted != 4 {
		t.Fatalf("deleted = %d", deleted)
	}
	missing := call(t, http.MethodDelete, "/api/monitors/5", "", db)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, body %s", missing.Code, missing.Body.String())
	}
	bad := call(t, http.MethodDelete, "/api/monitors/nope", "", db)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("bad id status = %d", bad.Code)
	}
}

func TestListChecksPassesLimit(t *testing.T) {
	t.Parallel()
	var gotLimit int
	db := fakeStore{
		listChecks: func(_ context.Context, monitorID int64, limit int) ([]monitor.CheckResult, error) {
			if monitorID != 4 {
				return nil, store.ErrNotFound
			}
			gotLimit = limit
			return []monitor.CheckResult{}, nil
		},
	}
	rec := call(t, http.MethodGet, "/api/monitors/4/checks", "", db)
	if rec.Code != http.StatusOK || gotLimit != 100 || strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("default status = %d limit = %d body = %s", rec.Code, gotLimit, rec.Body.String())
	}
	rec = call(t, http.MethodGet, "/api/monitors/4/checks?limit=25", "", db)
	if rec.Code != http.StatusOK || gotLimit != 25 {
		t.Fatalf("explicit status = %d limit = %d", rec.Code, gotLimit)
	}
	rec = call(t, http.MethodGet, "/api/monitors/4/checks?limit=0", "", db)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad limit status = %d", rec.Code)
	}
	rec = call(t, http.MethodGet, "/api/monitors/9/checks", "", db)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing monitor status = %d", rec.Code)
	}
}

func TestIncidentRoutes(t *testing.T) {
	t.Parallel()
	var gotStatus string
	db := fakeStore{
		listIncidents: func(_ context.Context, status string) ([]monitor.Incident, error) {
			gotStatus = status
			return []monitor.Incident{{ID: 1, Status: status, MonitorName: "Example"}}, nil
		},
		ack: func(_ context.Context, id int64) (monitor.Incident, error) {
			if id == 2 {
				return monitor.Incident{}, store.ErrNotOpen
			}
			return monitor.Incident{ID: id, Status: monitor.IncidentAcked, MonitorName: "Example"}, nil
		},
	}
	rec := call(t, http.MethodGet, "/api/incidents?status=open", "", db)
	if rec.Code != http.StatusOK || gotStatus != monitor.IncidentOpen {
		t.Fatalf("filter status = %d got %q body %s", rec.Code, gotStatus, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"monitor_name":"Example"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
	rec = call(t, http.MethodGet, "/api/incidents?status=nope", "", db)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad filter status = %d", rec.Code)
	}
	rec = call(t, http.MethodPost, "/api/incidents/8/ack", "", db)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"acked"`) {
		t.Fatalf("ack status = %d body %s", rec.Code, rec.Body.String())
	}
	rec = call(t, http.MethodPost, "/api/incidents/2/ack", "", db)
	if rec.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d body %s", rec.Code, rec.Body.String())
	}
}

func TestCORSAllowsTheDevServer(t *testing.T) {
	t.Parallel()
	preflight := call(t, http.MethodOptions, "/api/monitors", "", fakeStore{})
	if preflight.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d", preflight.Code)
	}
	if got := preflight.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("allow origin = %q", got)
	}
	if !strings.Contains(preflight.Header().Get("Access-Control-Allow-Methods"), "DELETE") {
		t.Fatalf("allow methods = %q", preflight.Header().Get("Access-Control-Allow-Methods"))
	}

	health := call(t, http.MethodGet, "/healthz", "", fakeStore{})
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d", health.Code)
	}
	if got := health.Header().Get("Access-Control-Allow-Origin"); got != allowedOrigin {
		t.Fatalf("health allow origin = %q", got)
	}
}

func call(t *testing.T, method, path, body string, db fakeStore) *httptest.ResponseRecorder {
	t.Helper()
	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), db, events.New())
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

type fakeStore struct {
	create        func(context.Context, monitor.Monitor) (monitor.Monitor, error)
	deleteMonitor func(context.Context, int64) error
	listChecks    func(context.Context, int64, int) ([]monitor.CheckResult, error)
	listIncidents func(context.Context, string) ([]monitor.Incident, error)
	ack           func(context.Context, int64) (monitor.Incident, error)
	resolve       func(context.Context, int64) (monitor.Incident, error)
}

func (f fakeStore) ListMonitorSummaries(context.Context) ([]monitor.MonitorSummary, error) {
	return []monitor.MonitorSummary{}, nil
}

func (f fakeStore) CreateMonitor(ctx context.Context, m monitor.Monitor) (monitor.Monitor, error) {
	if f.create == nil {
		return monitor.Monitor{}, errNotSet("CreateMonitor")
	}
	return f.create(ctx, m)
}

func (f fakeStore) DeleteMonitor(ctx context.Context, id int64) error {
	if f.deleteMonitor == nil {
		return errNotSet("DeleteMonitor")
	}
	return f.deleteMonitor(ctx, id)
}

func (f fakeStore) ListChecks(ctx context.Context, monitorID int64, limit int) ([]monitor.CheckResult, error) {
	if f.listChecks == nil {
		return nil, errNotSet("ListChecks")
	}
	return f.listChecks(ctx, monitorID, limit)
}

func (f fakeStore) ListIncidentsByStatus(ctx context.Context, status string) ([]monitor.Incident, error) {
	if f.listIncidents == nil {
		return nil, errNotSet("ListIncidentsByStatus")
	}
	return f.listIncidents(ctx, status)
}

func (f fakeStore) AckIncident(ctx context.Context, id int64) (monitor.Incident, error) {
	if f.ack == nil {
		return monitor.Incident{}, errNotSet("AckIncident")
	}
	return f.ack(ctx, id)
}

func (f fakeStore) ResolveIncident(ctx context.Context, id int64) (monitor.Incident, error) {
	if f.resolve == nil {
		return monitor.Incident{}, errNotSet("ResolveIncident")
	}
	return f.resolve(ctx, id)
}

func errNotSet(method string) error {
	return &notSetError{method: method}
}

type notSetError struct{ method string }

func (e *notSetError) Error() string { return e.method + " was not set on the fake store" }

func TestEventsStreamDeliversAndStopsWhenClientDisconnects(t *testing.T) {
	broker := events.New()
	srv := New(slog.New(slog.NewTextHandler(io.Discard, nil)), fakeStore{}, broker)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := &streamRecorder{hit: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		srv.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case <-rec.hit:
			goto delivered
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("event stream did not deliver a check")
		}
		broker.Publish(events.Event{Type: events.CheckResult, Data: map[string]int{"monitor_id": 1}})
		time.Sleep(10 * time.Millisecond)
	}
delivered:
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event stream did not stop when the client disconnected")
	}
	if body := rec.body(); !strings.Contains(body, "event: check_result") || !strings.Contains(body, `"monitor_id":1`) {
		t.Fatalf("stream = %q", body)
	}
}

// streamRecorder is an http.ResponseWriter that can be flushed and that signals
// once the event name has been written. The test reads the body only after the handler returns.
type streamRecorder struct {
	header http.Header
	buf    strings.Builder
	hit    chan struct{}
	once   sync.Once
	mu     sync.Mutex
}

func (w *streamRecorder) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *streamRecorder) WriteHeader(int) {}

func (w *streamRecorder) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "check_result") {
		w.once.Do(func() { close(w.hit) })
	}
	return n, err
}

func (w *streamRecorder) Flush() {}

func (w *streamRecorder) body() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}
