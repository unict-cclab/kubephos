package loadsession

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// A profile accepted at configuration time must be able to finish before the
// runtime timeout kills the plugin process. Previously a 4-hour profile (the
// timeline allows 24 hours) was accepted and the run was killed after 3 hours.
func TestValidateRejectsSessionsLongerThanTheRuntimeTimeout(t *testing.T) {
	spec := func(stepSeconds, warmup int, drain *int, action string) Spec {
		return Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: action, Replicas: 1, WarmupSeconds: warmup, DrainSeconds: drain, Steps: []LoadStep{{Type: "constant", DurationSeconds: stepSeconds, RPS: 10}}}
	}
	limit := maxTimedSessionSeconds()
	zero := 0
	for _, tc := range []struct {
		name  string
		spec  Spec
		valid bool
	}{
		{"four hours", spec(4*3600, 0, nil, "start"), false},
		{"fits exactly with default drain", spec(limit-defaultDrainSeconds, 0, nil, "start"), true},
		{"one second over with default drain", spec(limit-defaultDrainSeconds+1, 0, nil, "start"), false},
		{"warmup fits without drain", spec(limit-3600, 3600, &zero, "start"), true},
		{"warmup and drain push it over", spec(limit-3600, 3600, nil, "start"), false},
		{"resume keeps no timed phases", spec(4*3600, 0, nil, "resume"), true},
	} {
		raw, _ := json.Marshal(tc.spec)
		report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw})
		if report.Valid != tc.valid {
			t.Fatalf("%s: valid=%v %#v", tc.name, report.Valid, report.Issues)
		}
		if !tc.valid && (report.Issues[len(report.Issues)-1].Path != "steps" || !strings.Contains(report.Issues[len(report.Issues)-1].Message, "longer than the supported")) {
			t.Fatalf("%s: unexpected issue %#v", tc.name, report.Issues)
		}
	}
}

func TestSessionBudgetCountsCallChainProfiling(t *testing.T) {
	spec := Spec{Action: "start", WarmupSeconds: 60}
	duration := maxTimedSessionSeconds() - 60 - defaultDrainSeconds
	if err := checkSessionBudget(spec, duration, 0); err != nil {
		t.Fatalf("budget without profiling: %v", err)
	}
	err := checkSessionBudget(spec, duration, 120)
	if err == nil || !strings.Contains(err.Error(), "call-chain profiling 120s") {
		t.Fatalf("profiling must count against the budget: %v", err)
	}
}

// The budget mirrors the process timeout declared in plugin.yaml.
func TestSessionBudgetMatchesPluginRuntimeTimeout(t *testing.T) {
	raw, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Spec struct {
			Runtime struct {
				Timeout string `yaml:"timeout"`
			} `yaml:"runtime"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	timeout, err := time.ParseDuration(document.Spec.Runtime.Timeout)
	if err != nil || timeout != sessionRuntimeTimeout {
		t.Fatalf("plugin.yaml timeout %q differs from sessionRuntimeTimeout %s", document.Spec.Runtime.Timeout, sessionRuntimeTimeout)
	}
}
