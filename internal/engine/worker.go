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
	"kubephos.dev/kubephos/internal/storage"
)

type Worker struct {
	store       *storage.Store
	registry    *plugins.Registry
	artifacts   *artifacts.Client
	instanceID  string
	concurrency int
	poll        time.Duration
}

func NewWorker(store *storage.Store, registry *plugins.Registry, artifactStore *artifacts.Client, instanceID string, concurrency int, poll time.Duration) *Worker {
	return &Worker{store: store, registry: registry, artifacts: artifactStore, instanceID: instanceID, concurrency: concurrency, poll: poll}
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
		health, err := plugin.Precheck(runCtx, planned, log)
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
		result, err := plugin.Execute(runCtx, planned, log)
		if err != nil {
			w.stopOrFail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepVerifying, result, nil, ""); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		if err := w.store.SetOperationStatus(runCtx, operation.ID, domain.OperationVerifying, fmt.Sprintf("Verifying %s.", step.Name)); err != nil {
			w.fail(parent, operation.ID, step.ID, err)
			return
		}
		health, err = plugin.Verify(runCtx, planned, result, log)
		if err != nil {
			w.stopOrFail(parent, operation.ID, step.ID, err)
			return
		}
		healthRaw, _ = json.Marshal(health)
		if health.Status != domain.HealthHealthy {
			w.failWithHealth(parent, operation.ID, step.ID, healthRaw, fmt.Errorf("health gate failed: %s", health.Summary))
			return
		}
		if plugin.Manifest().HasCapability("infrastructure.discovery") {
			if err := w.captureDiscovery(runCtx, operation, result, log); err != nil {
				w.fail(parent, operation.ID, step.ID, err)
				return
			}
		}
		if err := w.store.SetStepState(runCtx, operation.ID, step.ID, domain.StepSucceeded, result, healthRaw, ""); err != nil {
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

func (w *Worker) captureDiscovery(ctx context.Context, operation domain.Operation, raw json.RawMessage, log plugins.Logger) error {
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
			ID:               id.New("res"),
			ProviderPluginID: operation.PluginID,
			ExternalID:       discovered.ExternalID,
			Kind:             discovered.Kind,
			Name:             discovered.Name,
			State:            discovered.State,
			Ownership:        "imported",
			Protection:       "read-only",
			Metadata:         discovered.Metadata,
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
