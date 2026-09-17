package strategyimage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestValidationAndPlan(t *testing.T) {
	raw := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_credential","name":"research-scheduler","kind":"scheduler","sourceImage":"ghcr.io/example/scheduler:v1.2.0","defaultConfiguration":{"configFile":"kind: KubeSchedulerConfiguration"}}`)
	plugin := Plugin{}
	report := plugin.Validate(context.Background(), Invocation{Input: raw})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || plan.Steps[0].Outputs[0].Type != "StrategyImage" || !plan.Steps[0].Mutating {
		t.Fatalf("unexpected plan %#v", plan)
	}
}

func TestPrecheckPinsSourceDigest(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	runner := &strategyRunner{digest: digest}
	plugin := Plugin{Runner: runner}
	step := plannedStrategyStep(t)
	health, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["sourceDigest"] != digest {
		t.Fatalf("unexpected precheck %#v %v", health, err)
	}
	if pinnedImage("ghcr.io/example/scheduler:v1", digest) != "ghcr.io/example/scheduler:v1@"+digest || pinnedImage("ghcr.io/example/scheduler@sha256:"+strings.Repeat("b", 64), digest) != "ghcr.io/example/scheduler@"+digest {
		t.Fatal("source image was not pinned to the inspected digest")
	}
}

func plannedStrategyStep(t *testing.T) domain.PlanStep {
	t.Helper()
	raw := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_credential","name":"research-scheduler","kind":"scheduler","sourceImage":"ghcr.io/example/scheduler:v1.2.0","defaultConfiguration":{}}`)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	endpoint, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "RegistryEndpoint", "spec": map[string]any{"protocol": "oci", "host": "harbor.example.test", "insecure": true}})
	credential, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "RegistryCredential", "metadata": map[string]string{"role": "push"}, "spec": map[string]any{"server": "harbor.example.test", "username": "robot", "password": "secret", "project": "kubephos", "scopes": []string{"pull", "push"}}})
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{
		"registry-endpoint":   {ID: "art_endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", Value: endpoint},
		"registry-credential": {ID: "art_credential", Type: "RegistryCredential", Version: "v1alpha1", Value: credential},
	}
	return step
}

type strategyRunner struct {
	digest string
}

func (runner *strategyRunner) Run(_ context.Context, _ []byte, _ ...string) (string, error) {
	return runner.digest, nil
}
