package api

import (
	"errors"
	"net/url"
	"strings"

	"uptime-monitor/internal/monitor"
)

const (
	maxNameLen       = 100
	maxURLLen        = 2048
	minIntervalSecs  = 5
	maxIntervalSecs  = 24 * 60 * 60
	defaultIntervalS = 30
)

type createMonitorRequest struct {
	Name            string `json:"name"`
	URL             string `json:"url"`
	IntervalSeconds *int   `json:"interval_seconds"`
}

// normalizeMonitor checks the create request and returns the monitor to insert.
// The error text is safe to show to the client. An omitted interval defaults to 30. An explicit 0 is rejected.
func normalizeMonitor(req createMonitorRequest) (monitor.Monitor, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return monitor.Monitor{}, errors.New("name is required")
	}
	if len(name) > maxNameLen {
		return monitor.Monitor{}, errors.New("name must be at most 100 characters")
	}

	rawURL := strings.TrimSpace(req.URL)
	if rawURL == "" {
		return monitor.Monitor{}, errors.New("url is required")
	}
	if len(rawURL) > maxURLLen {
		return monitor.Monitor{}, errors.New("url must be at most 2048 characters")
	}
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return monitor.Monitor{}, errors.New("url must be an absolute http or https URL")
	}

	interval := defaultIntervalS
	if req.IntervalSeconds != nil {
		interval = *req.IntervalSeconds
		if interval < minIntervalSecs || interval > maxIntervalSecs {
			return monitor.Monitor{}, errors.New("interval_seconds must be between 5 and 86400")
		}
	}

	return monitor.Monitor{
		Name:            name,
		URL:             rawURL,
		IntervalSeconds: interval,
	}, nil
}
