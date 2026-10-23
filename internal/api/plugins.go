package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"errors"
	"fmt"

	"log/slog"

	"net/http"

	"strconv"
	"strings"

	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/plugins"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) getPluginRuntime(response http.ResponseWriter, request *http.Request) {
	manager, ok := s.runtime.(runtimeConfigurator)
	if !ok {
		state, message := s.runtimeHealth(request.Context())
		writeJSON(response, http.StatusOK, map[string]any{"configured": state != "disabled", "status": state, "message": message})
		return
	}
	profile, err := manager.Profile(request.Context())
	if errors.Is(err, storage.ErrNotFound) {
		state, message := manager.State(request.Context())
		writeJSON(response, http.StatusOK, map[string]any{"configured": false, "status": state, "message": message})
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the OCI runtime profile.")
		return
	}
	state, message := manager.State(request.Context())
	writeJSON(response, http.StatusOK, map[string]any{"configured": true, "status": state, "message": message, "profile": profile})
}

func (s *Server) validatePluginRuntime(response http.ResponseWriter, request *http.Request) {
	manager, ok := s.runtime.(runtimeConfigurator)
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "runtime_unavailable", "Managed OCI runtime configuration is unavailable.")
		return
	}
	var profile domain.PluginRuntimeProfile
	if err := decodeJSON(request, &profile); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	err := manager.ValidateProfile(ctx, profile)
	cancel()
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "runtime_validation_failed", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"valid": true, "status": "healthy", "message": "Executor mutual TLS, rootless isolation, registry TLS and pull access are healthy."})
}

func (s *Server) activatePluginRuntime(response http.ResponseWriter, request *http.Request) {
	manager, ok := s.runtime.(runtimeConfigurator)
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "runtime_unavailable", "Managed OCI runtime configuration is unavailable.")
		return
	}
	var profile domain.PluginRuntimeProfile
	if err := decodeJSON(request, &profile); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	previous, previousErr := manager.Profile(request.Context())
	if previousErr != nil && !errors.Is(previousErr, storage.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the current OCI runtime profile.")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	profile, err := manager.ActivateProfile(ctx, profile)
	cancel()
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "runtime_validation_failed", err.Error())
		return
	}
	if err := s.reloadActivePluginPackages(request.Context()); err != nil {
		rollbackContext, rollbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
		var rollbackErr error
		if previousErr == nil {
			_, rollbackErr = s.store.SavePluginRuntimeProfile(rollbackContext, previous)
		} else {
			rollbackErr = s.store.DeletePluginRuntimeProfile(rollbackContext)
		}
		rollbackCancel()
		if rollbackErr != nil && !errors.Is(rollbackErr, storage.ErrNotFound) {
			writeError(response, http.StatusInternalServerError, "runtime_rollback_failed", "An installed package failed its runtime handshake and the previous runtime profile could not be restored.")
			return
		}
		writeError(response, http.StatusConflict, "plugin_load_failed", "Runtime activation was rolled back because an installed package failed its handshake: "+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"configured": true, "status": "healthy", "message": "Managed OCI runtime activated after all health gates passed.", "profile": profile})
}

func (s *Server) deactivatePluginRuntime(response http.ResponseWriter, request *http.Request) {
	manager, ok := s.runtime.(runtimeConfigurator)
	if !ok {
		writeError(response, http.StatusServiceUnavailable, "runtime_unavailable", "Managed OCI runtime configuration is unavailable.")
		return
	}
	if err := manager.DeactivateProfile(request.Context()); err != nil && !errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not deactivate the OCI runtime profile.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"configured": false, "status": "disabled", "message": "External OCI plugins are disabled. Managed infrastructure and artifacts were preserved."})
}

func (s *Server) reloadActivePluginPackages(ctx context.Context) error {
	if s.loadPlugin == nil {
		return errors.New("plugin loader is unavailable")
	}
	packages, err := s.store.ListActivePluginPackages(ctx)
	if err != nil {
		return err
	}
	loaded := make([]plugins.Plugin, 0, len(packages))
	for _, pluginPackage := range packages {
		descriptorDigest := sha256.Sum256([]byte(pluginPackage.Descriptor))
		if pluginPackage.DescriptorDigest != "sha256:"+hex.EncodeToString(descriptorDigest[:]) {
			return fmt.Errorf("plugin %s descriptor integrity check failed", pluginPackage.PluginID)
		}
		plugin, err := s.loadPlugin([]byte(pluginPackage.Descriptor))
		if err != nil {
			return err
		}
		if plugin.Manifest().ID != pluginPackage.PluginID || !plugin.Manifest().Matches(pluginPackage.Version, pluginPackage.Digest) {
			return fmt.Errorf("plugin %s metadata does not match its package", pluginPackage.PluginID)
		}
		loaded = append(loaded, plugin)
	}
	return s.registry.ReplaceExternal(loaded)
}

func (s *Server) listPlugins(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"items": s.registry.Manifests()})
}

func (s *Server) inspectPlugin(response http.ResponseWriter, request *http.Request) {
	descriptor, ok := decodePluginDescriptor(response, request)
	if !ok {
		return
	}
	manifest, err := plugins.InspectDefinition([]byte(descriptor))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	if s.registry.IsBundled(manifest.ID) {
		writeError(response, http.StatusConflict, "plugin_conflict", "A bundled plugin cannot be replaced.")
		return
	}
	runtimeContext, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	runtimeState, runtimeMessage := s.runtimeHealth(runtimeContext)
	cancel()
	policyAccepted := false
	policyMessage := runtimeMessage
	if runtimeState == "healthy" {
		if err := plugins.ValidateRuntimeImage(s.runtime, manifest.Runtime.Reference); err != nil {
			policyMessage = err.Error()
		} else {
			policyAccepted = true
			policyMessage = "Image registry and digest satisfy the executor policy."
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"manifest": manifest, "executorAvailable": runtimeState == "healthy", "policyAccepted": policyAccepted, "policyMessage": policyMessage,
	})
}

func (s *Server) importPlugin(response http.ResponseWriter, request *http.Request) {
	if state, _ := s.runtimeHealth(request.Context()); state != "healthy" || s.loadPlugin == nil {
		writeError(response, http.StatusServiceUnavailable, "runtime_unavailable", "Configure a dedicated OCI plugin executor before importing external plugins.")
		return
	}
	descriptor, ok := decodePluginDescriptor(response, request)
	if !ok {
		return
	}
	manifest, err := plugins.InspectDefinition([]byte(descriptor))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	if s.registry.IsBundled(manifest.ID) {
		writeError(response, http.StatusConflict, "plugin_conflict", "A bundled plugin cannot be replaced.")
		return
	}
	if err := plugins.ValidateRuntimeImage(s.runtime, manifest.Runtime.Reference); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	descriptorDigest := sha256.Sum256([]byte(descriptor))
	descriptorFingerprint := "sha256:" + hex.EncodeToString(descriptorDigest[:])
	job, err := s.store.CreatePluginImportJob(request.Context(), domain.PluginImportJob{
		ID: id.New("pimport"), Descriptor: descriptor, DescriptorDigest: descriptorFingerprint,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the plugin import.")
		return
	}
	writeJSON(response, http.StatusAccepted, job)
}

func decodePluginDescriptor(response http.ResponseWriter, request *http.Request) (string, bool) {
	var input struct {
		Descriptor string `json:"descriptor"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return "", false
	}
	input.Descriptor = strings.TrimSpace(input.Descriptor)
	if input.Descriptor == "" || len(input.Descriptor) > 512*1024 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", "The plugin descriptor must contain between 1 byte and 512 KB.")
		return "", false
	}
	return input.Descriptor, true
}

func (s *Server) listPluginImports(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListPluginImportJobs(request.Context(), 50)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list plugin import jobs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPluginImport(response http.ResponseWriter, request *http.Request) {
	job, err := s.store.GetPluginImportJob(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Plugin import job not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the plugin import job.")
		return
	}
	writeJSON(response, http.StatusOK, job)
}

func (s *Server) listPluginPackages(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListPluginPackages(request.Context(), 100)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list plugin package history.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) activatePluginPackage(response http.ResponseWriter, request *http.Request) {
	if state, _ := s.runtimeHealth(request.Context()); state != "healthy" || s.loadPlugin == nil {
		writeError(response, http.StatusServiceUnavailable, "runtime_unavailable", "Configure a dedicated OCI plugin executor before activating external plugins.")
		return
	}
	sequence, err := strconv.ParseInt(request.PathValue("sequence"), 10, 64)
	if err != nil || sequence <= 0 {
		writeError(response, http.StatusBadRequest, "invalid_package", "Plugin package sequence is invalid.")
		return
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	pluginPackage, err := s.store.GetPluginPackage(request.Context(), sequence)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Plugin package not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read plugin package.")
		return
	}
	descriptorDigest := sha256.Sum256([]byte(pluginPackage.Descriptor))
	if pluginPackage.DescriptorDigest != "sha256:"+hex.EncodeToString(descriptorDigest[:]) {
		writeError(response, http.StatusConflict, "integrity_failed", "Plugin descriptor integrity verification failed.")
		return
	}
	plugin, err := s.loadPlugin([]byte(pluginPackage.Descriptor))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	manifest := plugin.Manifest()
	if manifest.ID != pluginPackage.PluginID || !manifest.Matches(pluginPackage.Version, pluginPackage.Digest) || s.registry.IsBundled(manifest.ID) {
		writeError(response, http.StatusConflict, "plugin_conflict", "Stored plugin metadata no longer matches the validated package.")
		return
	}
	current, currentErr := s.store.GetActivePluginPackage(request.Context(), manifest.ID)
	if currentErr != nil && !errors.Is(currentErr, storage.ErrNotFound) {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not inspect the active plugin package.")
		return
	}
	activated, err := s.store.ActivatePluginPackage(request.Context(), pluginPackage)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not activate plugin package.")
		return
	}
	if err := s.registry.Install(plugin); err != nil {
		writeError(response, http.StatusConflict, "plugin_conflict", err.Error())
		return
	}
	if currentErr == nil && current.Digest != activated.Digest {
		s.evictPluginPackage(current)
	}
	writeJSON(response, http.StatusOK, activated)
}

func (s *Server) deactivatePluginPackage(response http.ResponseWriter, request *http.Request) {
	sequence, err := strconv.ParseInt(request.PathValue("sequence"), 10, 64)
	if err != nil || sequence <= 0 {
		writeError(response, http.StatusBadRequest, "invalid_package", "Plugin package sequence is invalid.")
		return
	}
	s.pluginMu.Lock()
	defer s.pluginMu.Unlock()
	pluginPackage, err := s.store.DeactivatePluginPackage(request.Context(), sequence)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Plugin package not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Only the active plugin package can be deactivated.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not deactivate plugin package.")
		return
	}
	if !s.registry.IsBundled(pluginPackage.PluginID) {
		if err := s.registry.Remove(pluginPackage.PluginID); err != nil {
			writeError(response, http.StatusConflict, "plugin_conflict", err.Error())
			return
		}
	}
	s.evictPluginPackage(pluginPackage)
	writeJSON(response, http.StatusOK, pluginPackage)
}

func (s *Server) evictPluginPackage(pluginPackage domain.PluginPackage) {
	cache, ok := s.runtime.(plugins.ContainerImageCache)
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
