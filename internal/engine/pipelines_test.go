package engine

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
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

func TestCleanupRestoresNetworkAfterLoadAndDeschedulerBeforeNamespace(t *testing.T) {
	stages := []domain.PipelineRunStage{{PluginID: "prepare"}, {PluginID: "io.kubephos.chaos.network.zones"}, {PluginID: "scheduler"}, {PluginID: "io.kubephos.applications.kubernetes.deploy"}, {PluginID: "descheduler"}, {PluginID: "load"}, {PluginID: "metrics"}}
	got := pipelineCleanupOrder(stages)
	want := []int{6, 5, 4, 1, 3, 2, 0}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	stages[3].PluginID = "other"
	if got := pipelineCleanupOrder(stages); !reflect.DeepEqual(got, []int{6, 5, 4, 3, 2, 1, 0}) {
		t.Fatalf("non-application order changed: %v", got)
	}
}
func TestFinalCleanupVerificationRequiresEveryHealthGate(t *testing.T) {
	plan := domain.Plan{Steps: []domain.PlanStep{{ID: "one", Cleanup: true}, {ID: "two", Cleanup: true}}}
	for _, tc := range []struct {
		name         string
		resolveError bool
		verifyError  bool
		remaining    bool
		want         bool
	}{{"all absent", false, false, false, true}, {"remaining resource", false, false, true, false}, {"API failure", false, true, false, false}, {"unverified input", true, false, false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(ctx context.Context, i int, s domain.PlanStep) (domain.PlanStep, error) {
				if tc.resolveError {
					return s, errors.New("artifact verification failed")
				}
				return s, nil
			}
			verify := func(ctx context.Context, s domain.PlanStep) (domain.HealthReport, error) {
				if tc.verifyError {
					return domain.HealthReport{}, errors.New("API unavailable")
				}
				status := domain.HealthHealthy
				if tc.remaining && s.ID == "two" {
					status = domain.HealthUnhealthy
				}
				return domain.HealthReport{Status: status}, nil
			}
			if got := verifyCleanupPlan(context.Background(), plan, resolve, verify); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if verifyCleanupPlan(context.Background(), domain.Plan{}, resolve, verify) {
				t.Fatal("empty plan accepted")
			}
		})
	}
}
