package metricscollector

import "testing"

// The final configured sample at completedAt keeps the last step's rate.
func TestConfiguredRateAtWindowEnd(t *testing.T) {
	var load loadSession
	load.Spec.Steps = []loadStep{{Type: "constant", DurationSeconds: 60, RPS: 20}, {Type: "constant", DurationSeconds: 60, RPS: 50}}
	if got := configuredRPS(load, 60); got != 50 {
		t.Fatalf("at the step boundary got %v, want the next step's 50", got)
	}
	if got := configuredRPS(load, 120); got != 50 {
		t.Fatalf("at the window end got %v, want 50", got)
	}
	if got := configuredRPS(load, 121); got != 0 {
		t.Fatalf("after the profile got %v, want 0", got)
	}
}
