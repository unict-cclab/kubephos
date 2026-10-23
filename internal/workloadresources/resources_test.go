package workloadresources

import "testing"

func TestOptionalRequestsAndLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]any
		fields int
	}{
		{"absent", map[string]any{}, 0},
		{"blank", map[string]any{"cpuRequest": "", "memoryLimit": ""}, 0},
		{"requests only", map[string]any{"cpuRequest": "0.5", "memoryRequest": "512Mi"}, 1},
		{"limits only", map[string]any{"cpuLimit": "1", "memoryLimit": "1Gi"}, 1},
		{"independent fields", map[string]any{"cpuRequest": "500m", "memoryLimit": "1Gi"}, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			resources, err := Build(test.values)
			if err != nil || len(resources) != test.fields {
				t.Fatalf("resources=%v error=%v", resources, err)
			}
		})
	}
}
func TestRejectInvalidOrInconsistentResources(t *testing.T) {
	for _, values := range []map[string]any{
		{"cpuRequest": "invalid"}, {"cpuLimit": "0"}, {"memoryLimit": "-1Gi"}, {"cpuRequest": 1},
		{"cpuRequest": "1500m", "cpuLimit": "1"}, {"memoryRequest": "1025Mi", "memoryLimit": "1Gi"},
	} {
		if _, err := Build(values); err == nil {
			t.Fatalf("accepted invalid resources %v", values)
		}
	}
	if _, err := Build(map[string]any{"cpuRequest": "1000m", "cpuLimit": "1", "memoryRequest": "1024Mi", "memoryLimit": "1Gi"}); err != nil {
		t.Fatal(err)
	}
}

func TestServiceOverridesInheritUnspecifiedDefaults(t *testing.T) {
	values := map[string]any{"cpuRequest": "250m", "cpuLimit": "1", "memoryRequest": "256Mi", "resourceOverrides": map[string]any{"m1": map[string]any{"cpuRequest": "500m", "memoryLimit": "1Gi"}}}
	if err := Validate(values, []string{"m1", "m2"}); err != nil {
		t.Fatal(err)
	}
	first, err := ForService(values, "m1")
	if err != nil {
		t.Fatal(err)
	}
	requests, limits := first["requests"].(map[string]any), first["limits"].(map[string]any)
	if requests["cpu"] != "500m" || requests["memory"] != "256Mi" || limits["cpu"] != "1" || limits["memory"] != "1Gi" {
		t.Fatalf("incorrect effective override: %v", first)
	}
	second, _ := ForService(values, "m2")
	if second["requests"].(map[string]any)["cpu"] != "250m" {
		t.Fatal("override leaked to another service")
	}
	for _, overrides := range []map[string]any{
		{"unknown": map[string]any{"cpuLimit": "1"}},
		{"m1": map[string]any{"unknownField": "1"}},
		{"m1": map[string]any{"cpuLimit": "100m"}},
		{"m1": "invalid"},
	} {
		values["resourceOverrides"] = overrides
		if err := Validate(values, []string{"m1", "m2"}); err == nil {
			t.Fatalf("accepted invalid overrides %v", overrides)
		}
	}
}
