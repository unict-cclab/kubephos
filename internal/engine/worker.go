package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

type Worker struct {
	store       *storage.Store
	registry    *plugins.Registry
	artifacts   *artifacts.Client
	vault       *secrets.Vault
	instanceID  string
	concurrency int
	poll        time.Duration
}

func NewWorker(store *storage.Store, registry *plugins.Registry, artifactStore *artifacts.Client, vault *secrets.Vault, instanceID string, concurrency int, poll time.Duration) *Worker {
	return &Worker{store: store, registry: registry, artifacts: artifactStore, vault: vault, instanceID: instanceID, concurrency: concurrency, poll: poll}
}

func (w *Worker) Run(ctx context.Context) error {
	if err := w.store.FailExpiredOperations(ctx); err != nil {
		return err
	}
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		w.expireLeases(ctx)
	}()
	for slot := 1; slot <= w.concurrency; slot++ {
		group.Add(1)
		go func(slot int) {
			defer group.Done()
			w.runSlot(ctx, slot)
		}(slot)
	}
	<-ctx.Done()
	group.Wait()
	return nil
}

func (w *Worker) runSlot(ctx context.Context, slot int) {
	owner := fmt.Sprintf("%s-%d", w.instanceID, slot)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		operation, found, err := w.store.ClaimOperation(ctx, owner)
		if err != nil {
			slog.Error("claim operation", "worker", owner, "error", err)
			w.wait(ctx)
			continue
		}
		if !found {
			w.wait(ctx)
			continue
		}
		w.process(ctx, owner, operation)
	}
}

func (w *Worker) process(parent context.Context, owner string, operation domain.Operation) {
	plugin, err := w.registry.Get(operation.PluginID)
	if err != nil {
		w.fail(parent, operation.ID, "", err)
		return
	}
	runCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go w.monitor(runCtx, cancel, done, owner, operation.ID)
	defer func() {
		close(done)
		cancel()
	}()
	if err := w.store.AppendLog(runCtx, operation.ID, "", "info", "worker", fmt.Sprintf("Worker %s claimed the operation.", owner)); err != nil {
		slog.Error("append operation log", "operation", operation.ID, "error", err)
	}
	for position, step := range operation.Steps {
		if err := runCtx.Err(); err != nil {
			w.stop(parent, operation.ID, step.ID, err)
			return
		}
		planned := operation.Plan.Steps[position]
		log := func(level, message string) error {
			return w.store.AppendLog(runCtx, operation.ID, step.ID, level, operation.PluginID, message)
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepPrechecking, nil, nil, ""); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetOperationStatus(runCtx, operation.ID, domain.OperationPrechecking, fmt.Sprintf("Prechecking %s.", step.Name)); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		runtimeStep, err := w.resolveArtifactInputs(runCtx, operation, position, planned)
		if err != nil {
			w.fail(parent, operation.ID, step.ID, fmt.Errorf("validate artifact inputs: %w", err))
			return
		}
		if len(runtimeStep.ResolvedInputs) > 0 {
			if err := log("info", fmt.Sprintf("Verified %d typed artifact input(s) before execution", len(runtimeStep.ResolvedInputs))); err != nil {
				w.fail(parent, operation.ID, step.ID, err)
				return
			}
		}
		health, err := plugin.Precheck(runCtx, runtimeStep, log)
		if err != nil {
			w.stopOrFail(parent, operation.ID, step.ID, err)
			return
		}
		healthRaw, _ := json.Marshal(health)
		if health.Status != domain.HealthHealthy {
			w.failWithHealth(parent, operation.ID, step.ID, healthRaw, fmt.Errorf("precheck failed: %s", health.Summary))
			return
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepRunning, nil, healthRaw, ""); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetOperationStatus(runCtx, operation.ID, domain.OperationRunning, fmt.Sprintf("Running %s.", step.Name)); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.reserveEffects(runCtx, operation, planned, log); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if planned.Cleanup {
			if err := w.runCleanupStep(runCtx, operation, step.ID, runtimeStep, plugin, healthRaw, log); err != nil {
				w.stopOrFail(parent, operation.ID, step.ID, err)
				return
			}
			continue
		}
		result, err := plugin.Execute(runCtx, runtimeStep, log)
		if err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.stopOrFail(parent, operation.ID, step.ID, err)
			return
		}
		persistedResult, err := persistedStepResult(planned, result)
		if err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepVerifying, persistedResult, nil, ""); err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetOperationStatus(runCtx, operation.ID, domain.OperationVerifying, fmt.Sprintf("Verifying %s.", step.Name)); err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		health, err = plugin.Verify(runCtx, runtimeStep, result, log)
		if err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.stopOrFail(parent, operation.ID, step.ID, err)
			return
		}
		healthRaw, _ = json.Marshal(health)
		if health.Status != domain.HealthHealthy {
			failure := w.cleanup(operation, runtimeStep, result, plugin, fmt.Errorf("health gate failed: %s", health.Summary))
			w.failWithHealth(parent, operation.ID, step.ID, healthRaw, failure)
			return
		}
		if err := w.persistStepOutputs(runCtx, operation, step, planned, result, log); err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, fmt.Errorf("persist typed outputs: %w", err))
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if plugin.Manifest().HasCapability("infrastructure.discovery") {
			if err := w.captureDiscovery(runCtx, operation, result, log); err != nil {
				w.fail(parent, operation.ID, step.ID, err)
				return
			}
		}
		if len(planned.Effects) > 0 {
			if err := w.completeEffects(runCtx, operation, planned, result); err != nil {
				err = w.cleanup(operation, runtimeStep, result, plugin, err)
				w.fail(parent, operation.ID, step.ID, err)
				return
			}
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepSucceeded, persistedResult, healthRaw, ""); err != nil {
			err = w.cleanup(operation, runtimeStep, result, plugin, err)
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
	}
	latest, err := w.store.GetOperation(runCtx, operation.ID)
	if err != nil {
		w.fail(runCtx, operation.ID, "", fmt.Errorf("read completed steps: %w", err))
		return
	}
	artifactValue, err := json.MarshalIndent(map[string]any{
		"operationId": operation.ID,
		"workspaceId": operation.WorkspaceID,
		"pluginId":    operation.PluginID,
		"planHash":    operation.PlanHash,
		"steps":       latest.Steps,
		"completedAt": time.Now().UTC(),
	}, "", "  ")
	if err != nil {
		w.fail(runCtx, operation.ID, "", err)
		return
	}
	stored, err := w.artifacts.Put(runCtx, artifacts.OperationKey(operation.ID, "result.json"), "result.json", "application/json", artifactValue)
	if err != nil {
		w.fail(runCtx, operation.ID, "", fmt.Errorf("store result artifact: %w", err))
		return
	}
	_, err = w.store.CreateArtifact(runCtx, domain.Artifact{
		ID:          id.New("art"),
		OperationID: operation.ID,
		Name:        "Result",
		Type:        "RunResult",
		Version:     "v1alpha1",
		MediaType:   "application/json",
		StorageKey:  stored.Key,
		Digest:      stored.Digest,
		SizeBytes:   stored.Size,
	})
	if err != nil {
		w.fail(runCtx, operation.ID, "", fmt.Errorf("save artifact metadata: %w", err))
		return
	}
	if err := w.store.AppendLog(runCtx, operation.ID, "", "info", "artifacts", "Result artifact stored and verified."); err != nil {
		slog.Error("append artifact log", "operation", operation.ID, "error", err)
	}
	if err := w.store.CompleteOperation(runCtx, operation.ID); err != nil {
		slog.Error("complete operation", "operation", operation.ID, "error", err)
	}
}

func (w *Worker) runCleanupStep(ctx context.Context, operation domain.Operation, stepID string, step domain.PlanStep, plugin plugins.Plugin, precheck json.RawMessage, log plugins.Logger) error {
	if err := plugin.Cleanup(ctx, step, nil, log); err != nil {
		return fmt.Errorf("cleanup execution failed: %w", err)
	}
	if err := w.store.SetStepState(ctx, operation.ID, stepID, domain.StepVerifying, nil, precheck, ""); err != nil {
		return err
	}
	if err := w.store.SetOperationStatus(ctx, operation.ID, domain.OperationVerifying, fmt.Sprintf("Verifying %s.", step.Name)); err != nil {
		return err
	}
	health, err := plugin.Verify(ctx, step, nil, log)
	if err != nil {
		return err
	}
	healthRaw, _ := json.Marshal(health)
	if health.Status != domain.HealthHealthy {
		return fmt.Errorf("cleanup health gate failed: %s", health.Summary)
	}
	if len(step.Effects) > 0 {
		if err := w.completeEffects(ctx, operation, step, json.RawMessage(`{"resources":[]}`)); err != nil {
			return err
		}
	}
	return w.store.SetStepState(ctx, operation.ID, stepID, domain.StepSucceeded, json.RawMessage(`{"cleanup":"completed"}`), healthRaw, "")
}

func persistedStepResult(step domain.PlanStep, result json.RawMessage) (json.RawMessage, error) {
	outputs := make([]map[string]any, 0, len(step.Outputs))
	for _, output := range step.Outputs {
		outputs = append(outputs, map[string]any{"name": output.Name, "type": output.Type, "version": output.Version, "sensitive": output.Sensitive})
	}
	if len(outputs) == 0 {
		return result, nil
	}
	return json.Marshal(map[string]any{"artifactOutputs": outputs})
}

func (w *Worker) resolveArtifactInputs(ctx context.Context, operation domain.Operation, position int, step domain.PlanStep) (domain.PlanStep, error) {
	if len(step.ArtifactInputs) == 0 {
		return step, nil
	}
	stepIDs := map[string]string{}
	for index := 0; index < position; index++ {
		stepIDs[operation.Plan.Steps[index].ID] = operation.Steps[index].ID
	}
	step.ResolvedInputs = make(map[string]domain.ResolvedArtifact, len(step.ArtifactInputs))
	for _, input := range step.ArtifactInputs {
		var artifact domain.Artifact
		var err error
		if input.ArtifactID != "" {
			artifact, err = w.store.GetArtifact(ctx, input.ArtifactID)
			if err == nil {
				var source domain.Operation
				source, err = w.store.GetOperation(ctx, artifact.OperationID)
				if err == nil && (source.WorkspaceID != operation.WorkspaceID || source.Status != domain.OperationSucceeded) {
					err = errors.New("external artifact does not belong to a successful operation in this workspace")
				}
			}
		} else {
			producerStepID, exists := stepIDs[input.FromStep]
			if !exists {
				return domain.PlanStep{}, fmt.Errorf("input %q references unavailable step %q", input.Name, input.FromStep)
			}
			artifact, err = w.store.GetArtifactByOutput(ctx, operation.ID, producerStepID, input.FromOutput)
		}
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("resolve input %q: %w", input.Name, err)
		}
		if artifact.Type != input.Type || artifact.Version != input.Version {
			return domain.PlanStep{}, fmt.Errorf("input %q expected %s/%s, received %s/%s", input.Name, input.Type, input.Version, artifact.Type, artifact.Version)
		}
		storedValue, err := w.artifacts.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("verify input %q: %w", input.Name, err)
		}
		value := storedValue
		if artifact.Sensitive {
			if w.vault == nil {
				return domain.PlanStep{}, fmt.Errorf("input %q cannot be decrypted because the artifact vault is unavailable", input.Name)
			}
			value, err = w.vault.DecryptBound(artifact.EncryptionNonce, storedValue, []byte(artifact.ID))
			if err != nil {
				return domain.PlanStep{}, fmt.Errorf("decrypt input %q: %w", input.Name, err)
			}
		}
		if err := artifacts.Verify(value, artifact.Digest, artifact.SizeBytes); err != nil {
			return domain.PlanStep{}, fmt.Errorf("verify plaintext input %q: %w", input.Name, err)
		}
		if artifact.MediaType == "application/json" && !json.Valid(value) {
			return domain.PlanStep{}, fmt.Errorf("input %q contains invalid JSON", input.Name)
		}
		step.ResolvedInputs[input.Name] = domain.ResolvedArtifact{
			ID: artifact.ID, Type: artifact.Type, Version: artifact.Version, MediaType: artifact.MediaType,
			Digest: artifact.Digest, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive, Value: value,
		}
	}
	return step, nil
}

func (w *Worker) persistStepOutputs(ctx context.Context, operation domain.Operation, step domain.OperationStep, planned domain.PlanStep, result json.RawMessage, log plugins.Logger) error {
	for _, output := range planned.Outputs {
		value, err := artifacts.ExtractJSON(result, output.Source)
		if err != nil {
			return fmt.Errorf("extract output %q: %w", output.Name, err)
		}
		artifactID := id.New("art")
		storageValue := []byte(value)
		storageMediaType := output.MediaType
		var encryptionNonce []byte
		if output.Sensitive {
			if w.vault == nil {
				return fmt.Errorf("encrypt output %q: artifact vault is unavailable", output.Name)
			}
			encryptionNonce, storageValue, err = w.vault.EncryptBound(value, []byte(artifactID))
			if err != nil {
				return fmt.Errorf("encrypt output %q: %w", output.Name, err)
			}
			storageMediaType = "application/octet-stream"
		}
		stored, err := w.artifacts.Put(ctx, artifacts.StepOutputKey(operation.ID, step.ID, output.Name), output.Name+".json", storageMediaType, storageValue)
		if err != nil {
			return fmt.Errorf("store output %q: %w", output.Name, err)
		}
		if _, err := w.artifacts.ReadVerified(ctx, stored.Key, stored.Digest, stored.Size); err != nil {
			return fmt.Errorf("verify stored output %q: %w", output.Name, err)
		}
		_, err = w.store.CreateArtifact(ctx, domain.Artifact{
			ID: artifactID, OperationID: operation.ID, StepID: step.ID, OutputName: output.Name,
			Name: output.Name, Type: output.Type, Version: output.Version, MediaType: output.MediaType,
			StorageKey: stored.Key, Digest: artifacts.Digest(value), SizeBytes: int64(len(value)),
			StorageDigest: stored.Digest, StoredSizeBytes: stored.Size, EncryptionNonce: encryptionNonce, Sensitive: output.Sensitive,
		})
		if err != nil {
			return fmt.Errorf("save output %q metadata: %w", output.Name, err)
		}
		if err := log("info", fmt.Sprintf("Stored and verified %s/%s output %s", output.Type, output.Version, output.Name)); err != nil {
			return err
		}
	}
	return nil
}

func (w *Worker) cleanup(operation domain.Operation, step domain.PlanStep, result json.RawMessage, plugin plugins.Plugin, cause error) error {
	if !step.Mutating && len(step.Effects) == 0 {
		return cause
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	log := func(level, message string) error {
		return w.store.AppendLog(ctx, operation.ID, "", level, operation.PluginID, message)
	}
	if err := plugin.Cleanup(ctx, step, result, log); err != nil {
		return errors.Join(cause, fmt.Errorf("cleanup failed: %w", err))
	}
	_ = w.store.SetOperationManagedResourcesState(ctx, operation.ID, "cleaned")
	return cause
}

func (w *Worker) reserveEffects(ctx context.Context, operation domain.Operation, step domain.PlanStep, log plugins.Logger) error {
	plugin, err := w.registry.Get(operation.PluginID)
	if err != nil {
		return err
	}
	provider := plugin.Manifest().Provider
	if provider == "" && len(step.Effects) > 0 {
		return errors.New("resource effects require a provider identity")
	}
	creates := make([]domain.InfrastructureResource, 0, len(step.Effects))
	for _, effect := range step.Effects {
		if effect.Action == "create" {
			resourceID := id.New("res")
			creates = append(creates, domain.InfrastructureResource{ID: resourceID, Provider: provider, ExternalID: effect.ExternalID, WorkspaceID: operation.WorkspaceID, Kind: effect.Kind, Name: effect.Name, Ownership: "managed", Protection: "managed"})
		}
	}
	createIDs, err := w.store.ReserveManagedResources(ctx, creates, operation.ID)
	if err != nil {
		return err
	}
	release := func() {
		_ = w.store.ReleaseManagedResourceReservations(ctx, creates, operation.ID)
	}
	for _, effect := range step.Effects {
		switch effect.Action {
		case "create":
			details, _ := json.Marshal(map[string]any{"operationId": operation.ID, "externalId": effect.ExternalID, "state": "reserved"})
			if err := w.store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "worker", Action: "infrastructure.reserve", TargetType: effect.Kind, TargetID: createIDs[effect.ExternalID], Outcome: "succeeded", Details: details}); err != nil {
				release()
				return err
			}
			if err := log("info", fmt.Sprintf("Reserved managed resource %s before execution", effect.ExternalID)); err != nil {
				release()
				return err
			}
		case "delete":
			if err := w.store.AuthorizeManagedResource(ctx, provider, effect.ExternalID, operation.WorkspaceID); err != nil {
				return err
			}
			if err := log("warning", fmt.Sprintf("Authorized deletion of managed resource %s", effect.ExternalID)); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported resource effect %q", effect.Action)
		}
	}
	return nil
}

func (w *Worker) completeEffects(ctx context.Context, operation domain.Operation, step domain.PlanStep, raw json.RawMessage) error {
	plugin, err := w.registry.Get(operation.PluginID)
	if err != nil {
		return err
	}
	provider := plugin.Manifest().Provider
	var result domain.DiscoveryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return fmt.Errorf("decode managed resource result: %w", err)
	}
	resources := map[string]domain.DiscoveredResource{}
	for _, resource := range result.Resources {
		resources[resource.ExternalID] = resource
	}
	for _, effect := range step.Effects {
		switch effect.Action {
		case "create":
			resource, ok := resources[effect.ExternalID]
			if !ok {
				return fmt.Errorf("managed resource result is missing %s", effect.ExternalID)
			}
			if err := w.store.CompleteManagedResource(ctx, operation.ID, provider, resource); err != nil {
				return err
			}
			details, _ := json.Marshal(map[string]any{"operationId": operation.ID, "externalId": effect.ExternalID, "state": resource.State})
			if err := w.store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "worker", Action: "infrastructure.create", TargetType: effect.Kind, TargetID: effect.ExternalID, Outcome: "succeeded", Details: details}); err != nil {
				return err
			}
		case "delete":
			if err := w.store.SetManagedResourceState(ctx, provider, effect.ExternalID, operation.WorkspaceID, "deleted"); err != nil {
				return err
			}
			details, _ := json.Marshal(map[string]any{"operationId": operation.ID, "externalId": effect.ExternalID, "state": "deleted"})
			if err := w.store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "worker", Action: "infrastructure.delete", TargetType: effect.Kind, TargetID: effect.ExternalID, Outcome: "succeeded", Details: details}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (w *Worker) captureDiscovery(ctx context.Context, operation domain.Operation, raw json.RawMessage, log plugins.Logger) error {
	plugin, err := w.registry.Get(operation.PluginID)
	if err != nil {
		return err
	}
	provider := plugin.Manifest().Provider
	if provider == "" {
		return errors.New("infrastructure discovery requires a provider identity")
	}
	var discovery domain.DiscoveryResult
	if err := json.Unmarshal(raw, &discovery); err != nil {
		return fmt.Errorf("decode discovery result: %w", err)
	}
	if len(discovery.Resources) == 0 {
		return nil
	}
	resources := make([]domain.InfrastructureResource, 0, len(discovery.Resources))
	for _, discovered := range discovery.Resources {
		if discovered.ExternalID == "" || discovered.Kind == "" || discovered.Name == "" {
			return errors.New("discovery result contains an incomplete resource identity")
		}
		resources = append(resources, domain.InfrastructureResource{
			ID:         id.New("res"),
			Provider:   provider,
			ExternalID: discovered.ExternalID,
			Kind:       discovered.Kind,
			Name:       discovered.Name,
			State:      discovered.State,
			Ownership:  "imported",
			Protection: "read-only",
			Metadata:   discovered.Metadata,
		})
	}
	if err := w.store.SyncDiscoveredResources(ctx, resources); err != nil {
		return fmt.Errorf("persist discovered resources: %w", err)
	}
	details, _ := json.Marshal(map[string]any{"operationId": operation.ID, "count": len(resources), "ownership": "imported", "protection": "read-only"})
	if err := w.store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "worker", Action: "infrastructure.discovery", TargetType: "provider", TargetID: operation.PluginID, Outcome: "succeeded", Details: details}); err != nil {
		return fmt.Errorf("audit discovery: %w", err)
	}
	return log("info", fmt.Sprintf("Inventory ledger updated with %d imported read-only resource(s)", len(resources)))
}

func (w *Worker) monitor(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, owner, operationID string) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			requested, err := w.store.OperationCancelRequested(ctx, operationID)
			if err != nil {
				slog.Error("check cancellation", "operation", operationID, "error", err)
				continue
			}
			if requested {
				cancel()
				return
			}
			if err := w.store.RenewLease(ctx, operationID, owner); err != nil {
				slog.Error("renew lease", "operation", operationID, "error", err)
			}
		}
	}
}

func (w *Worker) fail(ctx context.Context, operationID, stepID string, err error) {
	w.failWithHealth(ctx, operationID, stepID, nil, err)
}

func (w *Worker) failWithHealth(ctx context.Context, operationID, stepID string, health json.RawMessage, err error) {
	if stateErr := w.store.SetOperationManagedResourcesState(ctx, operationID, "failed"); stateErr != nil {
		slog.Error("fail managed resources", "operation", operationID, "error", stateErr)
	}
	if stepID != "" {
		if stateErr := w.store.SetStepState(ctx, operationID, stepID, domain.StepFailed, nil, health, err.Error()); stateErr != nil {
			slog.Error("fail step", "operation", operationID, "step", stepID, "error", stateErr)
		}
	}
	if stateErr := w.store.FailOperation(ctx, operationID, domain.OperationFailed, err.Error()); stateErr != nil {
		slog.Error("fail operation", "operation", operationID, "error", stateErr)
	}
}

func (w *Worker) stopOrFail(ctx context.Context, operationID, stepID string, err error) {
	if errors.Is(err, context.Canceled) {
		w.stop(ctx, operationID, stepID, err)
		return
	}
	w.fail(ctx, operationID, stepID, err)
}

func (w *Worker) stop(ctx context.Context, operationID, stepID string, cause error) {
	stateCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requested, err := w.store.OperationCancelRequested(stateCtx, operationID)
	if err == nil && requested {
		if stepID != "" {
			_ = w.store.SetStepState(stateCtx, operationID, stepID, domain.StepCanceled, nil, nil, "Canceled by user.")
		}
		_ = w.store.FailOperation(stateCtx, operationID, domain.OperationCanceled, "Operation canceled by user.")
		return
	}
	if errors.Is(cause, context.Canceled) {
		w.fail(stateCtx, operationID, stepID, errors.New("worker stopped before the current step completed; review logs and state before retrying"))
		return
	}
	w.fail(stateCtx, operationID, stepID, cause)
}

func (w *Worker) expireLeases(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.store.FailExpiredOperations(ctx); err != nil {
				slog.Error("expire worker leases", "error", err)
			}
		}
	}
}

func (w *Worker) wait(ctx context.Context) {
	timer := time.NewTimer(w.poll)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
