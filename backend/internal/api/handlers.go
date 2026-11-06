package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"uptime-monitor/internal/events"
	"uptime-monitor/internal/monitor"
)

const maxBodyBytes = 1 << 20

// handleCreateMonitor validates a JSON body and inserts a monitor.
// A bad body is 400. Success is 201 with status "unknown", because nothing has been probed yet.
func (s *Server) handleCreateMonitor(w http.ResponseWriter, r *http.Request) {
	mon, err := decodeMonitor(w, r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	created, err := s.store.CreateMonitor(r.Context(), mon)
	if err != nil {
		s.writeError(w, err)
		return
	}
	writeJSON(s.logger, w, http.StatusCreated, monitor.MonitorSummary{
		Monitor: created,
		Status:  monitor.StatusUnknown,
	})
}

func decodeMonitor(w http.ResponseWriter, r *http.Request) (monitor.Monitor, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var req createMonitorRequest
	if err := dec.Decode(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return monitor.Monitor{}, errors.New("request body is required")
		}
		return monitor.Monitor{}, err
	}
	if dec.More() {
		return monitor.Monitor{}, errors.New("request body must contain a single JSON object")
	}
	return normalizeMonitor(req)
}

// handleDeleteMonitor removes a monitor and its checks and incidents. Success is 204 with an empty body.
func (s *Server) handleDeleteMonitor(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.store.DeleteMonitor(r.Context(), id); err != nil {
		s.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleListChecks writes recent probes for one monitor, oldest of that window first.
// The default limit is 100. A missing monitor is 404.
func (s *Server) handleListChecks(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit, err := checkLimit(r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	checks, err := s.store.ListChecks(r.Context(), id, limit)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if checks == nil {
		checks = []monitor.CheckResult{}
	}
	writeJSON(s.logger, w, http.StatusOK, checks)
}

// handleListIncidents writes incidents, optionally filtered by status.
// An omitted status returns every incident.
func (s *Server) handleListIncidents(w http.ResponseWriter, r *http.Request) {
	status, err := incidentStatusFilter(r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	incidents, err := s.store.ListIncidentsByStatus(r.Context(), status)
	if err != nil {
		s.writeError(w, err)
		return
	}
	if incidents == nil {
		incidents = []monitor.Incident{}
	}
	writeJSON(s.logger, w, http.StatusOK, incidents)
}

// handleAckIncident acknowledges an open incident and writes the updated row.
func (s *Server) handleAckIncident(w http.ResponseWriter, r *http.Request) {
	s.transitionIncident(w, r, s.store.AckIncident)
}

// handleResolveIncident manually resolves an open or acknowledged incident.
func (s *Server) handleResolveIncident(w http.ResponseWriter, r *http.Request) {
	s.transitionIncident(w, r, s.store.ResolveIncident)
}

// transitionIncident is the shared body of ack and resolve.
// transition is the store method to call. AckIncident and ResolveIncident have the same signature.
// A successful change is published as incident_updated so other open dashboards reload.
func (s *Server) transitionIncident(w http.ResponseWriter, r *http.Request, transition func(context.Context, int64) (monitor.Incident, error)) {
	id, err := pathID(r)
	if err != nil {
		writeJSON(s.logger, w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	inc, err := transition(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.publish(events.IncidentUpdated, inc)
	writeJSON(s.logger, w, http.StatusOK, inc)
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, errors.New("id must be a positive integer")
	}
	return id, nil
}

func checkLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 100, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 1000 {
		return 0, errors.New("limit must be an integer from 1 to 1000")
	}
	return n, nil
}

func incidentStatusFilter(r *http.Request) (string, error) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", monitor.IncidentOpen, monitor.IncidentAcked, monitor.IncidentResolved:
		return status, nil
	default:
		return "", errors.New("status must be open, acked, or resolved")
	}
}
