package applicationdeployer

import (
	"context"
	"encoding/json"
	"fmt"
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

type createOnlyNamespaceRunner struct {
	namedNamespaceRunner
	created int
}

func (r *createOnlyNamespaceRunner) Run(ctx context.Context, k string, in []byte, args ...string) (string, error) {
	if len(args) > 0 && args[0] == "create" {
		r.created++
		if r.installed {
			return "", fmt.Errorf("namespace already exists")
		}
		args = append([]string{"apply"}, args[1:]...)
	}
	return r.namedNamespaceRunner.Run(ctx, k, in, args...)
}
func TestPersistentNamespaceNeverReplacesAnExistingNamespace(t *testing.T) {
	runner := &createOnlyNamespaceRunner{}
	plugin := Plugin{Runner: runner, StabilityChecks: 1}
	raw := json.RawMessage(`{"namespacePolicy":"create","applicationNamespace":"benchmark","clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`)
	plan, err := plugin.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	if _, err := plugin.Execute(context.Background(), step, discardLog); err != nil {
		t.Fatal(err)
	}
	if runner.created != 1 || runner.deleted != 0 {
		t.Fatal("namespace was not created exclusively")
	}
	marker := runner.marker
	if _, err := plugin.Execute(context.Background(), step, discardLog); err == nil {
		t.Fatal("existing namespace was overwritten")
	}
	if runner.deleted != 0 || runner.marker != marker {
		t.Fatal("existing namespace changed")
	}
	if err := plugin.Cleanup(context.Background(), step, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if runner.deleted != 1 {
		t.Fatal("persistent namespace cleanup missing")
	}
}

func TestPersistentNamespaceCleanupPreservesForeignNamespace(t *testing.T) {
	runner := &createOnlyNamespaceRunner{namedNamespaceRunner: namedNamespaceRunner{fakeRunner: fakeRunner{installed: true, marker: "another-owner"}}}
	plugin := Plugin{Runner: runner, StabilityChecks: 1}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"namespacePolicy":"create","applicationNamespace":"benchmark","clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status == "healthy" {
		t.Fatal("foreign namespace admitted", health, err)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != "healthy" {
		t.Fatal("safe cleanup blocked", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, discardLog)
	if err != nil || health.Status != "healthy" {
		t.Fatal("cleanup verification", health, err)
	}
	if runner.deleted != 0 || runner.marker != "another-owner" || !runner.installed {
		t.Fatal("foreign namespace was modified")
	}
}
