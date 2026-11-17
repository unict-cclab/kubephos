package workloadresources

import (
	"encoding/json"
	"testing"
)

func TestParseWaypoint(t *testing.T) {
	var values map[string]any
	if err := json.Unmarshal([]byte(`{"replicas":3,"cpuRequest":"200m","cpuLimit":"1","memoryRequest":"128Mi","memoryLimit":"256Mi","allServices":false,"serviceOverrides":{"frontend":true}}`), &values); err != nil {
		t.Fatal(err)
	}
	waypoint, err := ParseWaypoint(values)
	if err != nil {
		t.Fatal(err)
	}
	requests, _ := waypoint.Resources["requests"].(map[string]any)
	limits, _ := waypoint.Resources["limits"].(map[string]any)
	if waypoint.Replicas != 3 || requests["cpu"] != "200m" || requests["memory"] != "128Mi" || limits["cpu"] != "1" || limits["memory"] != "256Mi" {
		t.Fatalf("unexpected waypoint size: %+v", waypoint)
	}
	if waypoint.AllServices || !waypoint.ServiceOverrides["frontend"] || len(waypoint.ServiceOverrides) != 1 {
		t.Fatalf("unexpected waypoint services: %+v", waypoint)
	}
	if empty, err := ParseWaypoint(nil); err != nil || !empty.DefaultSize() || !empty.AllServices || len(empty.ServiceOverrides) != 0 {
		t.Fatalf("empty settings must keep Istio's defaults for every service: %+v %v", empty, err)
	}
	for name, invalid := range map[string]map[string]any{
		"zero replicas":       {"replicas": 0.0},
		"too many replicas":   {"replicas": float64(MaxWaypointReplicas + 1)},
		"fractional replicas": {"replicas": 1.5},
		"text replicas":       {"replicas": "2"},
		"unknown field":       {"proxies": 2.0},
		"bad quantity":        {"cpuRequest": "lots"},
		"request over limit":  {"memoryRequest": "1Gi", "memoryLimit": "512Mi"},
		"text default":        {"allServices": "yes"},
		"list overrides":      {"serviceOverrides": []any{"frontend"}},
		"text override":       {"serviceOverrides": map[string]any{"frontend": "off"}},
	} {
		if _, err := ParseWaypoint(invalid); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestParseWaypointPlacement(t *testing.T) {
	var values map[string]any
	if err := json.Unmarshal([]byte(`{"nodeSelector":{"kubernetes.io/hostname":"fg-01-control-plane-1"},"tolerations":[{"key":"node-role.kubernetes.io/control-plane","operator":"Exists","effect":"NoSchedule"}]}`), &values); err != nil {
		t.Fatal(err)
	}
	waypoint, err := ParseWaypoint(values)
	if err != nil {
		t.Fatal(err)
	}
	if waypoint.NodeSelector["kubernetes.io/hostname"] != "fg-01-control-plane-1" || len(waypoint.Tolerations) != 1 || waypoint.Tolerations[0].Operator != "Exists" {
		t.Fatalf("unexpected waypoint placement: %+v", waypoint)
	}
	if !waypoint.DefaultSize() || waypoint.DefaultDeployment() {
		t.Fatalf("a placed waypoint keeps Istio's size but not its generated deployment: %+v", waypoint)
	}
	for name, invalid := range map[string]map[string]any{
		"list selector":            {"nodeSelector": []any{"kubernetes.io/hostname"}},
		"number selector value":    {"nodeSelector": map[string]any{"zone": 1.0}},
		"invalid selector key":     {"nodeSelector": map[string]any{"bad key": "a"}},
		"object tolerations":       {"tolerations": map[string]any{"key": "a"}},
		"unknown toleration field": {"tolerations": []any{map[string]any{"key": "a", "operator": "Exists", "weight": 1.0}}},
		"bad operator":             {"tolerations": []any{map[string]any{"key": "a", "operator": "Near"}}},
		"exists with value":        {"tolerations": []any{map[string]any{"key": "a", "operator": "Exists", "value": "b"}}},
	} {
		if _, err := ParseWaypoint(invalid); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
