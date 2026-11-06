package monitor

import "time"

const (
	IncidentOpen     = "open"
	IncidentAcked    = "acked"
	IncidentResolved = "resolved"

	FailureThreshold = 3
)

const (
	DecisionNone    Decision = "none"
	DecisionOpen    Decision = "open"
	DecisionResolve Decision = "resolve"
	DecisionUpdate  Decision = "update"
)

type Decision string

type Incident struct {
	ID          int64      `json:"id"`
	MonitorID   int64      `json:"monitor_id"`
	Status      string     `json:"status"`
	OpenedAt    time.Time  `json:"opened_at"`
	AckedAt     *time.Time `json:"acked_at"`
	ResolvedAt  *time.Time `json:"resolved_at"`
	LastError   string     `json:"last_error"`
	MonitorName string     `json:"monitor_name,omitempty"`
}

// IsActive reports whether an incident should block opening another one.
// Open and acknowledged incidents are active. A nil incident is not.
func (in *Incident) IsActive() bool {
	if in == nil {
		return false
	}
	return in.Status == IncidentOpen || in.Status == IncidentAcked
}

// DecideIncident chooses the incident effect of one check. It does not write to the database.
// A success resolves an active incident. Three consecutive failures open one.
// Further failures while an incident is active only update its last error.
// consecutiveFailures counts failures newer than the latest resolve, including this check.
func DecideIncident(success bool, consecutiveFailures int, active *Incident) Decision {
	if success {
		if active.IsActive() {
			return DecisionResolve
		}
		return DecisionNone
	}
	if active.IsActive() {
		return DecisionUpdate
	}
	if consecutiveFailures >= FailureThreshold {
		return DecisionOpen
	}
	return DecisionNone
}
