package engine

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestPipelineStageHashIncludesResolvedInputsAndVersion(t *testing.T) {
	stage := domain.ResolvedPipelineStage{
		ID: "measure", PluginID: "metrics", PluginVersion: "1.0.0", Spec: json.RawMessage(`{"input":"art_one"}`),
		Plan: domain.Plan{PluginID: "metrics", Steps: []domain.PlanStep{{ID: "run", Name: "Run", Input: json.RawMessage(`{}`), ArtifactInputs: []domain.ArtifactInput{{Name: "input", Type: "Dataset", Version: "v1", ArtifactID: "art_one"}}}}},
	}
	first, err := pipelineStageHash(stage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := pipelineStageHash(stage)
	if err != nil || first != second || len(first) != 64 {
		t.Fatalf("expected deterministic hash, got %q and %q", first, second)
	}
	stage.PluginVersion = "1.0.1"
	changed, err := pipelineStageHash(stage)
	if err != nil || changed == first {
		t.Fatalf("plugin version did not change hash: %q", changed)
	}
	stage.PluginDigest = "sha256:" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	digestChanged, err := pipelineStageHash(stage)
	if err != nil || digestChanged == changed {
		t.Fatalf("plugin digest did not change hash: %q", digestChanged)
	}
}

func TestTerminalOperation(t *testing.T) {
	for _, status := range []string{domain.OperationSucceeded, domain.OperationFailed, domain.OperationCanceled} {
		if !terminalOperation(status) {
			t.Fatalf("expected %s to be terminal", status)
		}
	}
	if terminalOperation(domain.OperationRunning) {
		t.Fatal("running must not be terminal")
	}
}
