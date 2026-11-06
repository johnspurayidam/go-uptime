package checker

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"uptime-monitor/internal/monitor"
)

const (
	DefaultWorkers = 5

	checkTimeout        = 5 * time.Second
	defaultInterval     = 30 * time.Second
	defaultPollInterval = time.Second

	maxBodyBytes = 4 << 10
)

type MonitorSource interface {
	ListMonitors(ctx context.Context) ([]monitor.Monitor, error)
}

type Checker struct {
	logger       *slog.Logger
	client       *http.Client
	workers      int
	timeout      time.Duration
	pollInterval time.Duration
	onResult     func(monitor.CheckResult)
}

// New builds a checker with a shared HTTP client that does not follow redirects.
// onResult, when set, runs on a worker goroutine after each probe.
func New(logger *slog.Logger, workers int, onResult func(monitor.CheckResult)) *Checker {
	if workers < 1 {
		workers = DefaultWorkers
	}
	return &Checker{
		logger:       logger,
		workers:      workers,
		timeout:      checkTimeout,
		pollInterval: defaultPollInterval,
		onResult:     onResult,
		client: &http.Client{
			CheckRedirect: refuseRedirects,
		},
	}
}

// Run starts the worker pool and schedules probes until ctx is cancelled.
// It then waits for probes already queued to finish. source is re-read about once a second,
// so monitors created or deleted through the API are picked up without a restart.
func (c *Checker) Run(ctx context.Context, source MonitorSource) {
	jobs := make(chan monitor.Monitor, c.workers)

	var workers sync.WaitGroup
	for i := 0; i < c.workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			c.worker(jobs)
		}()
	}

	c.logger.Info("checker started", "workers", c.workers)
	c.schedule(ctx, jobs, source)

	close(jobs)
	workers.Wait()
	c.logger.Info("checker stopped")
}

// schedule is the single loop that decides which monitors are due.
// nextDue is used only by this goroutine.
func (c *Checker) schedule(ctx context.Context, jobs chan<- monitor.Monitor, source MonitorSource) {
	interval := c.pollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	nextDue := make(map[int64]time.Time)
	c.enqueueDue(ctx, jobs, source, nextDue)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.enqueueDue(ctx, jobs, source, nextDue)
		}
	}
}

// enqueueDue sends a job for every monitor whose next probe time has arrived.
// A monitor that is not in nextDue is due immediately, which is how a new URL is checked on the next pass.
func (c *Checker) enqueueDue(ctx context.Context, jobs chan<- monitor.Monitor, source MonitorSource, nextDue map[int64]time.Time) {
	monitors, err := source.ListMonitors(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Error("list monitors for checker", "error", err)
		}
		return
	}

	now := time.Now()
	seen := make(map[int64]struct{}, len(monitors))
	for _, m := range monitors {
		seen[m.ID] = struct{}{}
		if due, ok := nextDue[m.ID]; ok && now.Before(due) {
			continue
		}
		c.enqueue(ctx, jobs, m)
		nextDue[m.ID] = now.Add(intervalOf(m))
	}
	for id := range nextDue {
		if _, ok := seen[id]; !ok {
			delete(nextDue, id)
		}
	}
}

// enqueue sends a monitor to the worker pool, or returns when ctx is cancelled
// so a full job buffer cannot block shutdown.
func (c *Checker) enqueue(ctx context.Context, jobs chan<- monitor.Monitor, m monitor.Monitor) {
	select {
	case <-ctx.Done():
	case jobs <- m:
	}
}

// worker probes each monitor from the job channel, logs the outcome, and calls onResult.
func (c *Checker) worker(jobs <-chan monitor.Monitor) {
	for m := range jobs {
		result := c.check(m)
		c.logResult(m, result)
		if c.onResult != nil {
			c.onResult(result)
		}
	}
}

// check performs one HTTP GET and always returns a CheckResult.
// A down site, a timeout, and a connection error set Success to false. They are not Go errors.
// The timeout is independent of shutdown, so a probe that has started is allowed to finish.
func (c *Checker) check(m monitor.Monitor) monitor.CheckResult {
	result := monitor.CheckResult{
		MonitorID: m.ID,
		CheckedAt: time.Now().UTC(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	start := time.Now()
	resp, err := c.client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBodyBytes))

	result.StatusCode = resp.StatusCode
	result.Success = resp.StatusCode >= 200 && resp.StatusCode < 400
	if !result.Success {
		result.Error = resp.Status
	}
	return result
}

func (c *Checker) logResult(m monitor.Monitor, result monitor.CheckResult) {
	attrs := []any{
		"monitor_id", m.ID,
		"name", m.Name,
		"url", m.URL,
		"status_code", result.StatusCode,
		"latency_ms", result.LatencyMs,
		"success", result.Success,
	}
	if result.Success {
		c.logger.Info("check", attrs...)
		return
	}
	c.logger.Warn("check", append(attrs, "error", result.Error)...)
}

func intervalOf(m monitor.Monitor) time.Duration {
	if m.IntervalSeconds <= 0 {
		return defaultInterval
	}
	return time.Duration(m.IntervalSeconds) * time.Second
}

// refuseRedirects stops the HTTP client on the first response, so a 3xx is stored as success
// instead of being followed to the next URL.
func refuseRedirects(req *http.Request, via []*http.Request) error {
	return http.ErrUseLastResponse
}
