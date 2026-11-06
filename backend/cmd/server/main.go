package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"uptime-monitor/internal/api"
	"uptime-monitor/internal/checker"
	"uptime-monitor/internal/events"
	"uptime-monitor/internal/monitor"
	"uptime-monitor/internal/store"
)

// main starts the process. A startup or shutdown error is printed and the process exits 1.
// Cleanup runs in run, because os.Exit skips deferred calls in main.
func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	Addr   string
	DBPath string
}

// loadConfig reads ADDR and DB_PATH. Empty values become :8080 and uptime.db.
// A relative database path is resolved from the directory the process was started in.
func loadConfig() config {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "uptime.db"
	}
	return config{Addr: addr, DBPath: dbPath}
}

// run opens the database, serves the API, and probes monitors until the process is signaled.
// Ctrl+C stops the scheduler, waits up to 6 seconds for probes already running, closes live event
// streams, then shuts down HTTP with a 5 second limit. The database closes after that.
func run() error {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("close db", "error", err)
		}
	}()

	inserted, err := st.SeedMonitors(context.Background(), seedMonitors())
	if err != nil {
		return err
	}
	if inserted > 0 {
		logger.Info("seeded monitors", "count", inserted)
	}

	pruned, err := st.PruneChecks(context.Background(), time.Now().UTC().Add(-store.UptimeWindow))
	if err != nil {
		return err
	}
	if pruned > 0 {
		logger.Info("pruned checks", "count", pruned)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	broker := events.New()
	defer broker.Close()
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           api.New(logger, st, broker).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("listening", "addr", cfg.Addr, "db", cfg.DBPath)

	serverErr := make(chan error, 1)
	go func() {
		serveErr := httpServer.Serve(ln)
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			serverErr <- fmt.Errorf("serve: %w", serveErr)
			return
		}
		serverErr <- nil
	}()

	chk := checker.New(logger, checker.DefaultWorkers, func(result monitor.CheckResult) {
		saved, decision, err := st.RecordCheck(context.Background(), result)
		if err != nil {
			logger.Error("record check", "monitor_id", result.MonitorID, "error", err)
			return
		}
		broker.Publish(events.Event{Type: events.CheckResult, Data: saved})
		publishIncident(logger, broker, st, saved.MonitorID, decision)
		switch decision {
		case monitor.DecisionOpen:
			logger.Warn("incident opened", "monitor_id", result.MonitorID, "error", result.Error)
		case monitor.DecisionResolve:
			logger.Info("incident resolved", "monitor_id", result.MonitorID)
		}
	})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		chk.Run(ctx, st)
	}()
	go pruneHourly(ctx, logger, st)

	select {
	case err := <-serverErr:
		stop()
		waitForChecker(logger, &wg)
		broker.Close()
		return err
	case <-ctx.Done():
		logger.Info("shutting down")
		waitForChecker(logger, &wg)
		broker.Close()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown http server: %w", err)
		}
		return nil
	}
}

// publishIncident tells subscribers when a probe opened an incident or changed the current one.
func publishIncident(logger *slog.Logger, broker *events.Broker, st *store.Store, monitorID int64, decision monitor.Decision) {
	if decision == monitor.DecisionNone {
		return
	}
	incidents, err := st.ListIncidents(context.Background(), monitorID)
	if err != nil {
		logger.Error("load incident for event", "monitor_id", monitorID, "error", err)
		return
	}
	if len(incidents) == 0 {
		return
	}
	incident := incidents[len(incidents)-1]
	kind := events.IncidentUpdated
	if decision == monitor.DecisionOpen {
		kind = events.IncidentOpened
	}
	broker.Publish(events.Event{Type: kind, Data: incident})
}

// pruneHourly deletes checks older than 24 hours, once an hour, until ctx is cancelled.
// The process also prunes once during startup, before it accepts traffic.
func pruneHourly(ctx context.Context, logger *slog.Logger, st *store.Store) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n, err := st.PruneChecks(context.Background(), time.Now().UTC().Add(-store.UptimeWindow))
			if err != nil {
				logger.Error("prune checks", "error", err)
				continue
			}
			if n > 0 {
				logger.Info("pruned checks", "count", n)
			}
		}
	}
}

// waitForChecker waits for probes that were already running. Each probe times out after 5 seconds,
// so this wait is bounded. Shutdown continues if the pool has not finished.
func waitForChecker(logger *slog.Logger, wg *sync.WaitGroup) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		logger.Warn("checker drain timed out")
	}
}

// seedMonitors is inserted only when the database has no monitors yet.
// IDs are left at zero so SQLite assigns them.
func seedMonitors() []monitor.Monitor {
	now := time.Now().UTC()
	return []monitor.Monitor{
		{
			Name:            "Example",
			URL:             "https://example.com",
			IntervalSeconds: 30,
			CreatedAt:       now,
		},
		{
			Name:            "Always failing",
			URL:             "https://httpstat.us/500",
			IntervalSeconds: 30,
			CreatedAt:       now,
		},
	}
}
