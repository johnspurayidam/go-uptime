package monitor

import "testing"

func TestDecideIncident(t *testing.T) {
	t.Parallel()

	open := &Incident{Status: IncidentOpen}
	acked := &Incident{Status: IncidentAcked}
	resolved := &Incident{Status: IncidentResolved}

	tests := []struct {
		name     string
		success  bool
		failures int
		active   *Incident
		want     Decision
	}{
		{name: "one failure does not open", success: false, failures: 1, want: DecisionNone},
		{name: "two failures do not open", success: false, failures: 2, want: DecisionNone},
		{name: "the third failure opens", success: false, failures: 3, want: DecisionOpen},
		{name: "a longer streak opens when nothing is active", success: false, failures: 5, want: DecisionOpen},
		{name: "a failure while open updates that incident", success: false, failures: 4, active: open, want: DecisionUpdate},
		{name: "a failure while acked does not open another", success: false, failures: 4, active: acked, want: DecisionUpdate},
		{name: "two failures after a resolved incident do not open", success: false, failures: 2, active: resolved, want: DecisionNone},
		{name: "three failures after a resolved incident open again", success: false, failures: 3, active: resolved, want: DecisionOpen},
		{name: "success with no incident does nothing", success: true, want: DecisionNone},
		{name: "success resolves an open incident", success: true, active: open, want: DecisionResolve},
		{name: "success resolves an acked incident", success: true, active: acked, want: DecisionResolve},
		{name: "success after the incident is already resolved does nothing", success: true, active: resolved, want: DecisionNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DecideIncident(tt.success, tt.failures, tt.active)
			if got != tt.want {
				t.Fatalf("DecideIncident() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNilIncidentIsInactive(t *testing.T) {
	t.Parallel()
	var in *Incident
	if in.IsActive() {
		t.Fatal("nil incident reported active")
	}
}
