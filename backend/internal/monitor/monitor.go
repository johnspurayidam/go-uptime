package monitor

import "time"

type Monitor struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	URL             string    `json:"url"`
	IntervalSeconds int       `json:"interval_seconds"`
	CreatedAt       time.Time `json:"created_at"`
}

type CheckResult struct {
	ID         int64     `json:"id"`
	MonitorID  int64     `json:"monitor_id"`
	StatusCode int       `json:"status_code"`
	LatencyMs  int64     `json:"latency_ms"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
}

const (
	StatusUnknown = "unknown"
	StatusUp      = "up"
	StatusDown    = "down"
)

type MonitorSummary struct {
	Monitor
	Status        string   `json:"status"`
	LastLatencyMs *int64   `json:"last_latency_ms"`
	UptimePercent *float64 `json:"uptime_percent"`
}
