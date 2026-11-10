package workloadresources

import (
	"encoding/json"
	"testing"
)

func TestParseWaypoint(t *testing.T) {
	var values map[string]any
	if err := json.Unmarshal([]byte(`{"replicas":3,"cpuRequest":"200m","cpuLimit":"1","memoryRequest":"128Mi","memoryLimit":"256Mi"}`), &values); err != nil {
		t.Fatal(err)
	}
	waypoint, err := ParseWaypoint(values)
	if err != nil {
		t.Fatal(err)
	}
	requests, _ := waypoint.Resources["requests"].(map[string]any)
	limits, _ := waypoint.Resources["limits"].(map[string]any)
	if waypoint.Replicas != 3 || requests["cpu"] != "200m" || requests["memory"] != "128Mi" || limits["cpu"] != "1" || limits["memory"] != "256Mi" {
		t.Fatalf("unexpected waypoint: %+v", waypoint)
	}
	if empty, err := ParseWaypoint(nil); err != nil || !empty.IsDefault() {
		t.Fatalf("empty settings must keep Istio's defaults: %+v %v", empty, err)
	}
	for name, invalid := range map[string]map[string]any{
		"zero replicas":       {"replicas": 0.0},
		"too many replicas":   {"replicas": float64(MaxWaypointReplicas + 1)},
		"fractional replicas": {"replicas": 1.5},
		"text replicas":       {"replicas": "2"},
		"unknown field":       {"proxies": 2.0},
		"bad quantity":        {"cpuRequest": "lots"},
		"request over limit":  {"memoryRequest": "1Gi", "memoryLimit": "512Mi"},
	} {
		if _, err := ParseWaypoint(invalid); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
