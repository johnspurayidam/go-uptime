package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"uptime-monitor/internal/events"
	"uptime-monitor/internal/monitor"
	"uptime-monitor/internal/store"
)

type Store interface {
	ListMonitorSummaries(ctx context.Context) ([]monitor.MonitorSummary, error)
	CreateMonitor(ctx context.Context, m monitor.Monitor) (monitor.Monitor, error)
	DeleteMonitor(ctx context.Context, id int64) error
	ListChecks(ctx context.Context, monitorID int64, limit int) ([]monitor.CheckResult, error)
	ListIncidentsByStatus(ctx context.Context, status string) ([]monitor.Incident, error)
	AckIncident(ctx context.Context, id int64) (monitor.Incident, error)
	ResolveIncident(ctx context.Context, id int64) (monitor.Incident, error)
}

type Server struct {
	logger *slog.Logger
	store  Store
	broker *events.Broker
	mux    *http.ServeMux
}

// New builds the API server and registers its routes.
// broker receives probe and incident events and streams them to GET /api/events.
func New(logger *slog.Logger, store Store, broker *events.Broker) *Server {
	s := &Server{
		logger: logger,
		store:  store,
		broker: broker,
		mux:    http.NewServeMux(),
	}
	s.routes()
	return s
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /api/monitors", s.handleListMonitors)
	s.mux.HandleFunc("POST /api/monitors", s.handleCreateMonitor)
	s.mux.HandleFunc("DELETE /api/monitors/{id}", s.handleDeleteMonitor)
	s.mux.HandleFunc("GET /api/monitors/{id}/checks", s.handleListChecks)
	s.mux.HandleFunc("GET /api/incidents", s.handleListIncidents)
	s.mux.HandleFunc("POST /api/incidents/{id}/ack", s.handleAckIncident)
	s.mux.HandleFunc("POST /api/incidents/{id}/resolve", s.handleResolveIncident)
	s.mux.HandleFunc("GET /api/events", s.handleEvents)
}

// Handler returns the root handler. Request logging wraps CORS, which wraps the routes.
func (s *Server) Handler() http.Handler {
	return loggingMiddleware(s.logger, corsMiddleware(s.mux))
}

// handleHealth reports that the process is serving HTTP. It does not check the database.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(s.logger, w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleListMonitors writes every monitor with status, last latency, and 24 hour uptime.
func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	summaries, err := s.store.ListMonitorSummaries(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(s.logger, w, http.StatusOK, summaries)
}

// writeError maps a store error to an HTTP status.
// ErrNotFound is 404. ErrNotOpen and ErrNotActive are 409. Anything else is logged and returned as a generic 500.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(s.logger, w, http.StatusNotFound, map[string]string{"error": "not found"})
	case errors.Is(err, store.ErrNotOpen):
		writeJSON(s.logger, w, http.StatusConflict, map[string]string{"error": "incident is not open"})
	case errors.Is(err, store.ErrNotActive):
		writeJSON(s.logger, w, http.StatusConflict, map[string]string{"error": "incident is not open or acknowledged"})
	default:
		s.logger.Error("request failed", "error", err)
		writeJSON(s.logger, w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

// writeJSON sets the JSON content type and encodes body. An encode failure is logged,
// because the status line is already written.
func writeJSON(logger *slog.Logger, w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("write json response", "error", err)
	}
}
