package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"uptime-monitor/internal/events"
)

// handleEvents streams server-sent events until the client disconnects or the broker closes.
// Each event is one probe or one incident change. A keepalive comment keeps the connection from looking idle.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "retry: 3000\n\n")
	flusher.Flush()

	stream, cancel := s.broker.Subscribe()
	defer cancel()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-stream:
			if !ok {
				return
			}
			payload, err := json.Marshal(event.Data)
			if err != nil {
				s.logger.Error("encode event", "type", event.Type, "error", err)
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, payload)
			flusher.Flush()
		}
	}
}

// publish sends one event to every connected dashboard. A nil broker skips it.
func (s *Server) publish(eventType string, data any) {
	if s.broker == nil {
		return
	}
	s.broker.Publish(events.Event{Type: eventType, Data: data})
}
