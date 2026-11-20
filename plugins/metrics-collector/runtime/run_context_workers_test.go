package metricscollector

import "testing"

// A completed finite session has replicas 0; the worker count comes from workers.
func TestRunContextWorkersFromCompletedSession(t *testing.T) {
	var load loadSession
	load.Spec.Replicas = 0
	load.Spec.Workers = 3
	load.Spec.Clock = "shared-start"
	if context := loadSessionContext(load); context == nil || context.Workers != 3 {
		t.Fatalf("run context %+v", context)
	}
	load.Spec.Workers = 0
	load.Spec.Replicas = 2
	if context := loadSessionContext(load); context == nil || context.Workers != 2 {
		t.Fatalf("legacy run context %+v", context)
	}
}
