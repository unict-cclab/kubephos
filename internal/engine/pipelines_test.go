package engine

import (
	"encoding/json"
	"errors"
	"strings"
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

func TestCleanupStageRequiresOnlyCleanupSteps(t *testing.T) {
	if cleanupStage(domain.Plan{}) {
		t.Fatal("empty plan cannot be a cleanup stage")
	}
	if !cleanupStage(domain.Plan{Steps: []domain.PlanStep{{ID: "one", Cleanup: true}, {ID: "two", Cleanup: true}}}) {
		t.Fatal("expected an all-cleanup plan")
	}
	if cleanupStage(domain.Plan{Steps: []domain.PlanStep{{ID: "cleanup", Cleanup: true}, {ID: "run"}}}) {
		t.Fatal("mixed plans cannot bypass runtime configuration validation")
	}
}

func TestFailedCleanupDoesNotBlockEarlierStages(t *testing.T) {
	stages := []domain.PipelineRunStage{
		{Title: "Deploy isolated application", CleanupStatus: domain.StepPending},
		{Title: "Network injection", CleanupStatus: domain.StepFailed, CleanupError: "reset failed"},
	}
	failures := []error{}
	next := -1
	for position := len(stages) - 1; position >= 0; position-- {
		complete, failure := cleanupStageResult(stages[position])
		if failure != nil {
			failures = append(failures, failure)
		}
		if !complete {
			next = position
			break
		}
	}
	if next != 0 {
		t.Fatalf("expected application cleanup at position 0, got %d", next)
	}
	if err := errors.Join(failures...); err == nil || !strings.Contains(err.Error(), "Network injection") {
		t.Fatalf("expected the failed cleanup to be retained, got %v", err)
	}
}
