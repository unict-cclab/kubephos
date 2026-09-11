package pluginimports

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
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/storage"
)

type Worker struct {
	store      *storage.Store
	registry   *plugins.Registry
	runtime    plugins.ContainerRunner
	loadPlugin func([]byte) (plugins.Plugin, error)
	owner      string
	poll       time.Duration
}

func NewWorker(store *storage.Store, registry *plugins.Registry, runtime plugins.ContainerRunner, loadPlugin func([]byte) (plugins.Plugin, error), owner string, poll time.Duration) *Worker {
	return &Worker{store: store, registry: registry, runtime: runtime, loadPlugin: loadPlugin, owner: owner, poll: poll}
}

func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		job, found, err := w.store.ClaimPluginImportJob(ctx, w.owner)
		if err != nil {
			slog.Error("claim plugin import", "worker", w.owner, "error", err)
			w.wait(ctx)
			continue
		}
		if !found {
			w.wait(ctx)
			continue
		}
		w.process(ctx, job)
	}
}

func (w *Worker) process(ctx context.Context, job domain.PluginImportJob) {
	done := make(chan struct{})
	go w.renewLease(ctx, job.ID, done)
	defer close(done)
	fail := func(err error) {
		if ctx.Err() != nil {
			return
		}
		message := strings.TrimSpace(err.Error())
		if len(message) > 2048 {
			message = message[:2048]
		}
		if storeErr := w.store.FailPluginImportJob(context.WithoutCancel(ctx), job.ID, w.owner, message); storeErr != nil && !errors.Is(storeErr, storage.ErrConflict) {
			slog.Error("fail plugin import", "job", job.ID, "error", storeErr)
		}
	}
	manifest, err := plugins.InspectDefinition([]byte(job.Descriptor))
	if err != nil {
		fail(fmt.Errorf("descriptor inspection failed: %w", err))
		return
	}
	if w.registry.IsBundled(manifest.ID) {
		fail(fmt.Errorf("bundled plugin %q cannot be replaced", manifest.ID))
		return
	}
	manifestRaw, err := json.Marshal(manifest)
	if err != nil {
		fail(err)
		return
	}
	if err := w.store.SetPluginImportJobState(ctx, job.ID, w.owner, "validating", 40, "Checking executor health, image policy and plugin protocol.", manifestRaw, manifest.ID, manifest.Version, manifest.Runtime.Digest); err != nil {
		fail(err)
		return
	}
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = w.runtime.Ready(readyCtx)
	cancel()
	if err != nil {
		fail(fmt.Errorf("OCI executor health check failed: %w", err))
		return
	}
	if err := plugins.ValidateRuntimeImage(w.runtime, manifest.Runtime.Reference); err != nil {
		fail(fmt.Errorf("image policy check failed: %w", err))
		return
	}
	plugin, err := w.loadPlugin([]byte(job.Descriptor))
	if err != nil {
		fail(fmt.Errorf("plugin protocol validation failed: %w", err))
		return
	}
	validated := plugin.Manifest()
	if validated.ID != manifest.ID || !validated.Matches(manifest.Version, manifest.Runtime.Digest) {
		fail(errors.New("validated plugin metadata changed during the import"))
		return
	}
	if err := w.store.SetPluginImportJobState(ctx, job.ID, w.owner, "activating", 85, "All validation gates passed. Activating the package atomically.", manifestRaw, manifest.ID, manifest.Version, manifest.Runtime.Digest); err != nil {
		fail(err)
		return
	}
	current, currentErr := w.store.GetActivePluginPackage(ctx, manifest.ID)
	if currentErr != nil && !errors.Is(currentErr, storage.ErrNotFound) {
		fail(fmt.Errorf("inspect active package: %w", currentErr))
		return
	}
	descriptorDigest := sha256.Sum256([]byte(job.Descriptor))
	descriptorFingerprint := "sha256:" + hex.EncodeToString(descriptorDigest[:])
	if currentErr == nil && current.Version == manifest.Version && current.Digest == manifest.Runtime.Digest && current.DescriptorDigest != descriptorFingerprint {
		fail(errors.New("changed descriptors require a new plugin version or image digest"))
		return
	}
	activated, err := w.store.CompletePluginImportJob(ctx, job.ID, w.owner, domain.PluginPackage{
		PluginID: manifest.ID, Version: manifest.Version, Digest: manifest.Runtime.Digest, Descriptor: job.Descriptor,
		DescriptorDigest: descriptorFingerprint, Active: true,
	})
	if err != nil {
		fail(fmt.Errorf("activate package: %w", err))
		return
	}
	if err := w.registry.Install(plugin); err != nil {
		slog.Error("refresh imported plugin", "job", job.ID, "plugin", manifest.ID, "error", err)
	}
	if currentErr == nil && current.Digest != activated.Digest {
		w.evict(current)
	}
}

func (w *Worker) renewLease(ctx context.Context, id string, done <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-ticker.C:
			if err := w.store.RenewPluginImportJobLease(ctx, id, w.owner); err != nil && !errors.Is(err, storage.ErrConflict) {
				slog.Error("renew plugin import lease", "job", id, "error", err)
			}
		}
	}
}

func (w *Worker) evict(pluginPackage domain.PluginPackage) {
	cache, ok := w.runtime.(plugins.ContainerImageCache)
	if !ok {
		return
	}
	manifest, err := plugins.InspectDefinition([]byte(pluginPackage.Descriptor))
	if err != nil || manifest.Runtime.Reference == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := cache.EvictImage(ctx, manifest.Runtime.Reference); err != nil {
		slog.Warn("evict plugin image", "plugin", pluginPackage.PluginID, "digest", pluginPackage.Digest, "error", err)
	}
}

func (w *Worker) wait(ctx context.Context) {
	delay := w.poll
	if delay <= 0 {
		delay = time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
