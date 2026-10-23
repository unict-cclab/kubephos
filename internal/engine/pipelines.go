package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
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
	defer func() {
		if err := w.store.SyncExperimentPipelineRun(context.WithoutCancel(ctx), run.ID); err != nil {
			slog.Error("sync experiment trial", "run", run.ID, "error", err)
		}
	}()
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
	if run.TerminalStatus != "" {
		complete, cleanupErr := w.reconcilePipelineCleanup(ctx, owner, run)
		if cleanupErr != nil {
			run.Error = errors.Join(errors.New(run.Error), cleanupErr).Error()
			if err := w.store.FailPipelineRun(ctx, run.ID, owner, run.TerminalStatus, run.Error); err != nil {
				return err
			}
			released = true
			return nil
		}
		if !complete {
			release()
			return nil
		}
		if err := w.store.FailPipelineRun(ctx, run.ID, owner, run.TerminalStatus, run.Error); err != nil {
			return err
		}
		released = true
		return nil
	}
	produced := map[string]map[string]string{}
	for position := range run.Stages {
		runStage := run.Stages[position]
		if runStage.OperationID == "" {
			if run.CancelRequested {
				return w.deferPipelineTermination(ctx, owner, run, pipeline, position, domain.OperationCanceled, errors.New("canceled before the next stage started"))
			}
			if err := w.startPipelineStage(ctx, owner, run, pipeline, position, produced); err != nil {
				return w.deferPipelineTermination(ctx, owner, run, pipeline, position, domain.OperationFailed, err)
			}
			release()
			return nil
		}
		operation, err := w.store.GetOperation(ctx, runStage.OperationID)
		if err != nil {
			return w.deferPipelineTermination(ctx, owner, run, pipeline, position, domain.OperationFailed, fmt.Errorf("load stage operation: %w", err))
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
			return w.deferPipelineTermination(ctx, owner, run, pipeline, position, domain.OperationFailed, fmt.Errorf("stage %q failed: %s", runStage.Title, operation.Error))
		case domain.OperationCanceled:
			return w.deferPipelineTermination(ctx, owner, run, pipeline, position, domain.OperationCanceled, errors.New("stage operation was canceled"))
		default:
			release()
			return nil
		}
	}
	if run.ResultType == "" && run.ResultVersion == "" {
		if err := w.store.CompletePipelineRun(ctx, run.ID, owner, ""); err != nil {
			return err
		}
		released = true
		return nil
	}
	resultOutputs := produced[pipeline.Definition.Result.Stage]
	artifactID := resultOutputs[pipeline.Definition.Result.Output]
	if artifactID == "" {
		return w.deferPipelineTermination(ctx, owner, run, pipeline, len(run.Stages)-1, domain.OperationFailed, errors.New("validated result artifact was not produced"))
	}
	artifact, err := w.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return w.deferPipelineTermination(ctx, owner, run, pipeline, len(run.Stages)-1, domain.OperationFailed, fmt.Errorf("load result artifact: %w", err))
	}
	if artifact.Type != run.ResultType || artifact.Version != run.ResultVersion || artifact.VerifiedAt.IsZero() {
		return w.deferPipelineTermination(ctx, owner, run, pipeline, len(run.Stages)-1, domain.OperationFailed, errors.New("result artifact does not satisfy the validated contract"))
	}
	if pipeline.Definition.CleanupAfterRun {
		complete, err := w.reconcilePipelineCleanup(ctx, owner, run)
		if err != nil {
			return w.failPipelineRun(ctx, owner, run, -1, err)
		}
		if !complete {
			release()
			return nil
		}
	}
	if err := w.store.CompletePipelineRun(ctx, run.ID, owner, artifactID); err != nil {
		return err
	}
	released = true
	return nil
}

func (w *Worker) reconcilePipelineCleanup(ctx context.Context, owner string, run domain.PipelineRun) (bool, error) {
	for _, position := range pipelineCleanupOrder(run.Stages) {
		stage := run.Stages[position]
		if complete, _ := cleanupStageResult(stage); complete {
			continue
		}
		if stage.OperationID == "" {
			if err := w.store.SkipPipelineRunStageCleanup(ctx, run.ID, stage.ID, owner); err != nil {
				return false, err
			}
			continue
		}
		if stage.CleanupOperationID == "" {
			source, err := w.store.GetOperation(ctx, stage.OperationID)
			if err != nil {
				return false, fmt.Errorf("load cleanup source for stage %q: %w", stage.Title, err)
			}
			plan, err := workflows.CleanupPlan(source)
			if errors.Is(err, workflows.ErrNoCleanupSteps) {
				if err := w.store.SkipPipelineRunStageCleanup(ctx, run.ID, stage.ID, owner); err != nil {
					return false, err
				}
				continue
			}
			if err != nil {
				return false, fmt.Errorf("plan cleanup for stage %q: %w", stage.Title, err)
			}
			plugin, err := w.registry.Get(source.PluginID)
			if err != nil {
				return false, err
			}
			manifest := plugin.Manifest()
			if !manifest.Matches(source.PluginVersion, source.PluginDigest) || !manifest.HasCapability("lifecycle.cleanup") {
				return false, fmt.Errorf("stage %q cleanup capability or plugin identity changed", stage.Title)
			}
			validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "info", Path: "pipeline", Message: "Cleanup was generated from the completed validated stage operation."}}}
			hash, err := pipelineStageHash(domain.ResolvedPipelineStage{PluginID: source.PluginID, PluginVersion: source.PluginVersion, PluginDigest: source.PluginDigest, Spec: source.Spec, Plan: plan})
			if err != nil {
				return false, err
			}
			_, err = w.store.CreatePipelineCleanupOperation(ctx, run.ID, stage.ID, owner, domain.Operation{
				ID: id.New("op"), WorkspaceID: run.WorkspaceID, PluginID: source.PluginID,
				PluginVersion: source.PluginVersion, PluginDigest: source.PluginDigest,
				Title: run.Name + " · Reset · " + stage.Title, Status: domain.OperationQueued,
				Spec: source.Spec, Plan: plan, Validation: validation, PlanHash: hash,
			})
			return false, err
		}
		operation, err := w.store.GetOperation(ctx, stage.CleanupOperationID)
		if err != nil {
			return false, fmt.Errorf("load cleanup for stage %q: %w", stage.Title, err)
		}
		if err := w.store.SyncPipelineRunStageCleanup(ctx, stage.ID, operation); err != nil {
			return false, err
		}
		stage.CleanupStatus = operation.Status
		stage.CleanupError = operation.Error
		run.Stages[position] = stage
		if complete, _ := cleanupStageResult(stage); complete {
			continue
		}
		return false, nil
	}

	// All remaining cleanups have now finished. Only namespace timeout failures
	// may be recovered, and only after the original typed health gate passes.
	failures := []error{}
	for _, stage := range run.Stages {
		if stage.CleanupStatus == domain.StepFailed && stage.PluginID == "io.kubephos.applications.kubernetes.deploy" && strings.Contains(stage.CleanupError, "namespace cleanup timed out") {
			recovered, err := w.recheckNamespaceCleanup(ctx, run.ID, owner, stage)
			if err != nil {
				return false, err
			}
			if recovered {
				continue
			}
		}
		if _, failure := cleanupStageResult(stage); failure != nil {
			failures = append(failures, failure)
		}
	}
	return true, errors.Join(failures...)
}

func cleanupStageResult(stage domain.PipelineRunStage) (bool, error) {
	switch stage.CleanupStatus {
	case domain.StepSucceeded, "skipped":
		return true, nil
	case domain.StepFailed:
		message := strings.TrimSpace(stage.CleanupError)
		if message == "" {
			message = "cleanup operation failed"
		}
		return true, fmt.Errorf("cleanup for stage %q failed: %s", stage.Title, message)
	case domain.StepCanceled:
		return true, fmt.Errorf("cleanup for stage %q was canceled", stage.Title)
	default:
		return false, nil
	}
}

func (w *Worker) deferPipelineTermination(ctx context.Context, owner string, run domain.PipelineRun, pipeline domain.Pipeline, position int, status string, cause error) error {
	if position >= 0 && position < len(run.Stages) {
		stageStatus := domain.StepFailed
		if status == domain.OperationCanceled {
			stageStatus = domain.StepCanceled
		}
		if err := w.store.FailPipelineRunStage(ctx, run.Stages[position].ID, stageStatus, cause.Error()); err != nil {
			return err
		}
	}
	if !pipeline.Definition.CleanupAfterRun {
		return w.store.FailPipelineRun(ctx, run.ID, owner, status, cause.Error())
	}
	return w.store.BeginPipelineRunTermination(ctx, run.ID, owner, status, cause.Error())
}

func (w *Worker) startPipelineStage(ctx context.Context, owner string, run domain.PipelineRun, pipeline domain.Pipeline, position int, produced map[string]map[string]string) error {
	resolved, err := workflows.ResolveStage(pipeline, position, produced)
	if err != nil {
		return err
	}
	resolved, err = workflows.ResolveRuntimeTokens(resolved, map[string]string{domain.RuntimeExecutionIDToken: strings.ReplaceAll(run.ID, "_", "-")})
	if err != nil {
		return err
	}
	plugin, err := w.registry.Get(resolved.PluginID)
	if err != nil {
		return err
	}
	manifest := plugin.Manifest()
	if !manifest.Matches(resolved.PluginVersion, resolved.PluginDigest) || resolved.Plan.PluginID != resolved.PluginID {
		return fmt.Errorf("stage %q plugin version or identity changed", resolved.ID)
	}
	validation := domain.ValidationReport{Valid: true}
	if cleanupStage(resolved.Plan) {
		validation.Issues = []domain.ValidationIssue{{Level: "info", Path: "pipeline", Message: "Cleanup stage was generated from an immutable, previously validated lifecycle."}}
	} else {
		validation = plugin.Validate(ctx, resolved.Spec)
		if !validation.Valid {
			return fmt.Errorf("stage %q configuration is no longer valid", resolved.ID)
		}
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
		PluginVersion: resolved.PluginVersion, PluginDigest: resolved.PluginDigest,
		Title: run.Name + " · " + runStage.Title, Status: domain.OperationQueued,
		Spec: resolved.Spec, Plan: resolved.Plan, Validation: validation, PlanHash: hash,
	})
	return err
}

func cleanupStage(plan domain.Plan) bool {
	if len(plan.Steps) == 0 {
		return false
	}
	for _, step := range plan.Steps {
		if !step.Cleanup {
			return false
		}
	}
	return true
}

func pipelineStageHash(stage domain.ResolvedPipelineStage) (string, error) {
	payload, err := json.Marshal(struct {
		PluginID      string          `json:"pluginId"`
		PluginVersion string          `json:"pluginVersion"`
		PluginDigest  string          `json:"pluginDigest,omitempty"`
		Spec          json.RawMessage `json:"spec"`
		Plan          domain.Plan     `json:"plan"`
	}{PluginID: stage.PluginID, PluginVersion: stage.PluginVersion, PluginDigest: stage.PluginDigest, Spec: stage.Spec, Plan: stage.Plan})
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

func terminalOperation(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

// Preserve reverse dependency order, except that network shaping is restored
// after load/descheduler shutdown and before deleting application namespaces.
func pipelineCleanupOrder(stages []domain.PipelineRunStage) []int {
	order := make([]int, 0, len(stages))
	network := []int{}
	for i := len(stages) - 1; i >= 0; i-- {
		if stages[i].PluginID == "io.kubephos.chaos.network.zones" {
			network = append(network, i)
		} else {
			order = append(order, i)
		}
	}
	at := len(order)
	for i, index := range order {
		if stages[index].PluginID == "io.kubephos.applications.kubernetes.deploy" {
			at = i
			break
		}
	}
	if at == len(order) {
		result := make([]int, len(stages))
		for i := range result {
			result[i] = len(stages) - 1 - i
		}
		return result
	}
	result := append([]int{}, order[:at]...)
	result = append(result, network...)
	return append(result, order[at:]...)
}

func (w *Worker) recheckNamespaceCleanup(ctx context.Context, runID, owner string, stage domain.PipelineRunStage) (bool, error) {
	operation, err := w.store.GetOperation(ctx, stage.CleanupOperationID)
	if err != nil {
		return false, err
	}
	plugin, err := w.registry.Get(operation.PluginID)
	if err != nil {
		return false, err
	}
	if !plugin.Manifest().Matches(operation.PluginVersion, operation.PluginDigest) || !cleanupStage(operation.Plan) {
		return false, nil
	}
	checkCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := w.store.RenewPipelineRunLease(checkCtx, runID, owner); err != nil {
		return false, err
	}
	heartbeatDone := make(chan struct{})
	defer func() { cancel(); <-heartbeatDone }()
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-checkCtx.Done():
				return
			case <-ticker.C:
				if err := w.store.RenewPipelineRunLease(checkCtx, runID, owner); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	log := func(level, message string) error {
		return w.store.AppendLog(checkCtx, operation.ID, "", level, "cleanup-recheck", message)
	}
	if !verifyCleanupPlan(checkCtx, operation.Plan, func(ctx context.Context, position int, step domain.PlanStep) (domain.PlanStep, error) {
		return w.resolveArtifactInputs(ctx, operation, position, step)
	}, func(ctx context.Context, step domain.PlanStep) (domain.HealthReport, error) {
		return plugin.Verify(ctx, step, nil, log)
	}) {
		return false, nil
	}
	if err := log("warning", "The initial namespace cleanup timed out, but the final verified health gate confirms absence after all remaining cleanups. Pipeline cleanup recovered."); err != nil {
		return false, err
	}
	if err := w.store.RenewPipelineRunLease(checkCtx, runID, owner); err != nil {
		return false, err
	}
	// Retain the failed operation and its diagnostic logs as the audit record.
	operation.Status = domain.OperationSucceeded
	operation.Error = ""
	if err := w.store.SyncPipelineRunStageCleanup(ctx, stage.ID, operation); err != nil {
		return false, err
	}
	return true, nil
}

func verifyCleanupPlan(ctx context.Context, plan domain.Plan, resolve func(context.Context, int, domain.PlanStep) (domain.PlanStep, error), verify func(context.Context, domain.PlanStep) (domain.HealthReport, error)) bool {
	if !cleanupStage(plan) {
		return false
	}
	for position, planned := range plan.Steps {
		step, err := resolve(ctx, position, planned)
		if err != nil {
			return false
		}
		health, err := verify(ctx, step)
		if err != nil || health.Status != domain.HealthHealthy {
			return false
		}
	}
	return true
}
