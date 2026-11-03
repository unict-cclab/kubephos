package applicationdeployer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type namedNamespaceRunner struct {
	fakeRunner
	deleted int
}

func (r *namedNamespaceRunner) Run(ctx context.Context, k string, in []byte, args ...string) (string, error) {
	if strings.Contains(strings.Join(args, " "), "creationTimestamp") {
		return "2026-11-03T10:00:00Z", nil
	}
	if strings.HasPrefix(strings.Join(args, " "), "delete namespace benchmark ") {
		r.deleted++
	}
	return r.fakeRunner.Run(ctx, k, in, args...)
}

func TestCustomNamespaceIsResetOnlyDuringPreparationAndRemovedAfterRun(t *testing.T) {
	r := &namedNamespaceRunner{fakeRunner: fakeRunner{installed: true, marker: "old-run"}}
	p := Plugin{Runner: r, StabilityChecks: 1}
	spec := Spec{ApplicationNamespace: "benchmark", Mode: "prepare", ClusterConnectionRef: "art_cluster", ManifestSetRef: "art_manifests", WorkloadTargetsRef: "art_workloads", ServiceEndpointsRef: "art_endpoints"}
	raw, _ := json.Marshal(spec)
	plan, err := p.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	health, err := p.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("custom reset precheck: %#v %v", health, err)
	}
	if _, err := p.Execute(context.Background(), step, discardLog); err != nil {
		t.Fatal(err)
	}
	if r.deleted != 1 || !r.installed {
		t.Fatal("namespace was not recreated")
	}
	spec.Mode = "deploy"
	raw, _ = json.Marshal(spec)
	plan, err = p.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step = plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	if _, err := p.Execute(context.Background(), step, discardLog); err != nil {
		t.Fatal(err)
	}
	if r.deleted != 1 {
		t.Fatal("deploy erased the prepared namespace")
	}
	if err := p.Cleanup(context.Background(), step, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if r.deleted != 2 || r.installed {
		t.Fatal("namespace was not removed after the run")
	}
}

func TestNamespaceValidationAndDefaultCompatibility(t *testing.T) {
	for _, name := range []string{"default", "kube-system", "kubephos-system", "istio-system", "chaos-mesh", "monitoring", "Bad_Name", "-bad", strings.Repeat("a", 64)} {
		if ValidateApplicationNamespace(name) == nil {
			t.Errorf("unsafe namespace accepted: %s", name)
		}
	}
	for _, name := range []string{"", "benchmark", "my-app-1"} {
		if err := ValidateApplicationNamespace(name); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var input stepInput
	if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(input.Namespace, "kubephos-app-") || resetApplicationNamespace(input) {
		t.Fatal("default namespace behavior changed")
	}
}
