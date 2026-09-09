package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/workflows"
)

func (w *Worker) runPipelineCoordinator(ctx context.Context) {
	owner := w.instanceID + "-pipelines"
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		run, found, err := w.store.ClaimPipelineRun(ctx, owner)
		if err != nil {
			slog.Error("claim pipeline run", "worker", owner, "error", err)
			w.wait(ctx)
			continue
		}
		if !found {
			w.wait(ctx)
			continue
		}
		if err := w.reconcilePipelineRun(ctx, owner, run); err != nil {
			slog.Error("reconcile pipeline run", "run", run.ID, "error", err)
		}
	}
}

func (w *Worker) reconcilePipelineRun(ctx context.Context, owner string, run domain.PipelineRun) error {
	released := false
	release := func() {
		if !released {
			_ = w.store.ReleasePipelineRun(context.WithoutCancel(ctx), run.ID, owner)
			released = true
		}
	}
	defer release()
	pipeline, err := w.store.GetPipeline(ctx, run.PipelineID)
	if err != nil {
		return w.failPipelineRun(ctx, owner, run, -1, fmt.Errorf("load pipeline: %w", err))
	}
	if pipeline.Hash != run.PipelineHash || pipeline.WorkspaceID != run.WorkspaceID {
		return w.failPipelineRun(ctx, owner, run, -1, errors.New("validated pipeline identity changed"))
	}
	produced := map[string]map[string]string{}
	for position := range run.Stages {
		runStage := run.Stages[position]
		if runStage.OperationID == "" {
			if run.CancelRequested {
				return w.cancelPipelineRun(ctx, owner, run, position, "Canceled before the next stage started.")
			}
			if err := w.startPipelineStage(ctx, owner, run, pipeline, position, produced); err != nil {
				return w.failPipelineRun(ctx, owner, run, position, err)
			}
			release()
			return nil
		}
		operation, err := w.store.GetOperation(ctx, runStage.OperationID)
		if err != nil {
			return w.failPipelineRun(ctx, owner, run, position, fmt.Errorf("load stage operation: %w", err))
		}
		if err := w.store.SyncPipelineRunStage(ctx, runStage.ID, operation); err != nil {
			return err
		}
		if run.CancelRequested && !terminalOperation(operation.Status) {
			if err := w.store.RequestCancel(ctx, operation.ID); err != nil && !errors.Is(err, storage.ErrConflict) {
				return err
			}
			release()
			return nil
		}
		switch operation.Status {
		case domain.OperationSucceeded:
			outputs := map[string]string{}
			for _, artifact := range operation.Artifacts {
				if artifact.OutputName != "" {
					outputs[artifact.OutputName] = artifact.ID
				}
			}
			produced[runStage.StageID] = outputs
		case domain.OperationFailed:
			return w.failPipelineRun(ctx, owner, run, position, fmt.Errorf("stage %q failed: %s", runStage.Title, operation.Error))
		case domain.OperationCanceled:
			return w.cancelPipelineRun(ctx, owner, run, position, "Stage operation was canceled.")
		default:
			release()
			return nil
		}
	}
	resultOutputs := produced[pipeline.Definition.Result.Stage]
	artifactID := resultOutputs[pipeline.Definition.Result.Output]
	if artifactID == "" {
		return w.failPipelineRun(ctx, owner, run, len(run.Stages)-1, errors.New("validated result artifact was not produced"))
	}
	artifact, err := w.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return w.failPipelineRun(ctx, owner, run, len(run.Stages)-1, fmt.Errorf("load result artifact: %w", err))
	}
	if artifact.Type != run.ResultType || artifact.Version != run.ResultVersion || artifact.VerifiedAt.IsZero() {
		return w.failPipelineRun(ctx, owner, run, len(run.Stages)-1, errors.New("result artifact does not satisfy the validated contract"))
	}
	if err := w.store.CompletePipelineRun(ctx, run.ID, owner, artifactID); err != nil {
		return err
	}
	released = true
	return nil
}

func (w *Worker) startPipelineStage(ctx context.Context, owner string, run domain.PipelineRun, pipeline domain.Pipeline, position int, produced map[string]map[string]string) error {
	resolved, err := workflows.ResolveStage(pipeline, position, produced)
	if err != nil {
		return err
	}
	plugin, err := w.registry.Get(resolved.PluginID)
	if err != nil {
		return err
	}
	manifest := plugin.Manifest()
	if manifest.Version != resolved.PluginVersion || resolved.Plan.PluginID != resolved.PluginID {
		return fmt.Errorf("stage %q plugin version or identity changed", resolved.ID)
	}
	validation := plugin.Validate(ctx, resolved.Spec)
	if !validation.Valid {
		return fmt.Errorf("stage %q configuration is no longer valid", resolved.ID)
	}
	validation.CheckedAt = time.Now().UTC()
	validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "pipeline", Message: "Stage configuration and typed inputs were resolved from validated pipeline " + pipeline.Hash[:12] + "."})
	hash, err := pipelineStageHash(resolved)
	if err != nil {
		return err
	}
	runStage := run.Stages[position]
	_, err = w.store.CreatePipelineStageOperation(ctx, run.ID, runStage.ID, owner, domain.Operation{
		ID: id.New("op"), WorkspaceID: run.WorkspaceID, PluginID: resolved.PluginID,
		Title: run.Name + " · " + runStage.Title, Status: domain.OperationQueued,
		Spec: resolved.Spec, Plan: resolved.Plan, Validation: validation, PlanHash: hash,
	})
	return err
}

func pipelineStageHash(stage domain.ResolvedPipelineStage) (string, error) {
	payload, err := json.Marshal(struct {
		PluginID      string          `json:"pluginId"`
		PluginVersion string          `json:"pluginVersion"`
		Spec          json.RawMessage `json:"spec"`
		Plan          domain.Plan     `json:"plan"`
	}{PluginID: stage.PluginID, PluginVersion: stage.PluginVersion, Spec: stage.Spec, Plan: stage.Plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func (w *Worker) failPipelineRun(ctx context.Context, owner string, run domain.PipelineRun, position int, cause error) error {
	if position >= 0 && position < len(run.Stages) {
		if err := w.store.FailPipelineRunStage(ctx, run.Stages[position].ID, domain.StepFailed, cause.Error()); err != nil {
			return err
		}
	}
	return w.store.FailPipelineRun(ctx, run.ID, owner, domain.OperationFailed, cause.Error())
}

func (w *Worker) cancelPipelineRun(ctx context.Context, owner string, run domain.PipelineRun, position int, message string) error {
	if position >= 0 && position < len(run.Stages) {
		if err := w.store.FailPipelineRunStage(ctx, run.Stages[position].ID, domain.StepCanceled, message); err != nil {
			return err
		}
	}
	return w.store.FailPipelineRun(ctx, run.ID, owner, domain.OperationCanceled, message)
}

func terminalOperation(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}
