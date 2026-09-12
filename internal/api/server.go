package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
	"kubephos.dev/kubephos/internal/schema"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/webui"
	"kubephos.dev/kubephos/internal/workflows"
)

type Server struct {
	store          *storage.Store
	registry       *plugins.Registry
	artifacts      *artifacts.Client
	vault          *secrets.Vault
	runtime        plugins.ContainerRunner
	loadPlugin     func([]byte) (plugins.Plugin, error)
	version        string
	pluginMu       sync.Mutex
	terminalMu     sync.Mutex
	terminals      map[string]terminalTicket
	terminalActive map[string]int
	terminalSSH    *pluginssh.Client
}

type runtimeConfigurator interface {
	plugins.ContainerRunner
	State(context.Context) (string, string)
	Profile(context.Context) (domain.PluginRuntimeProfile, error)
	ValidateProfile(context.Context, domain.PluginRuntimeProfile) error
	ActivateProfile(context.Context, domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error)
	DeactivateProfile(context.Context) error
}

func NewServer(store *storage.Store, registry *plugins.Registry, artifactStore *artifacts.Client, vault *secrets.Vault, runtime plugins.ContainerRunner, loadPlugin func([]byte) (plugins.Plugin, error), version, webDirectory string) (http.Handler, error) {
	server := &Server{store: store, registry: registry, artifacts: artifactStore, vault: vault, runtime: runtime, loadPlugin: loadPlugin, version: version, terminals: map[string]terminalTicket{}, terminalActive: map[string]int{}, terminalSSH: pluginssh.New()}
	webHandler, err := webui.Handler(webDirectory)
	if err != nil {
		return nil, err
	}
	router := http.NewServeMux()
	router.HandleFunc("GET /health/live", server.live)
	router.HandleFunc("GET /health/ready", server.ready)
	router.HandleFunc("GET /api/v1/auth/status", server.authStatus)
	router.HandleFunc("POST /api/v1/auth/setup", server.authSetup)
	router.HandleFunc("POST /api/v1/auth/login", server.authLogin)
	router.HandleFunc("POST /api/v1/auth/logout", server.authLogout)
	router.HandleFunc("GET /api/v1/system", server.system)
	router.HandleFunc("GET /api/v1/plugins", server.listPlugins)
	router.HandleFunc("POST /api/v1/plugins/inspect", server.inspectPlugin)
	router.HandleFunc("POST /api/v1/plugins", server.importPlugin)
	router.HandleFunc("GET /api/v1/plugin-imports", server.listPluginImports)
	router.HandleFunc("GET /api/v1/plugin-imports/{id}", server.getPluginImport)
	router.HandleFunc("GET /api/v1/plugin-packages", server.listPluginPackages)
	router.HandleFunc("POST /api/v1/plugin-packages/{sequence}/activate", server.activatePluginPackage)
	router.HandleFunc("POST /api/v1/plugin-packages/{sequence}/deactivate", server.deactivatePluginPackage)
	router.HandleFunc("GET /api/v1/plugin-runtime", server.getPluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/validate", server.validatePluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/activate", server.activatePluginRuntime)
	router.HandleFunc("POST /api/v1/plugin-runtime/deactivate", server.deactivatePluginRuntime)
	router.HandleFunc("GET /api/v1/catalog/applications", server.listCatalogApplications)
	router.HandleFunc("POST /api/v1/catalog/applications", server.importCatalogApplication)
	router.HandleFunc("GET /api/v1/credentials", server.listCredentials)
	router.HandleFunc("POST /api/v1/credentials", server.createCredential)
	router.HandleFunc("GET /api/v1/connections", server.listConnections)
	router.HandleFunc("POST /api/v1/connections", server.createConnection)
	router.HandleFunc("DELETE /api/v1/connections/{id}", server.deleteConnection)
	router.HandleFunc("GET /api/v1/machine-templates", server.listMachineTemplates)
	router.HandleFunc("POST /api/v1/machine-templates", server.createMachineTemplate)
	router.HandleFunc("GET /api/v1/machine-templates/{id}", server.getMachineTemplate)
	router.HandleFunc("DELETE /api/v1/machine-templates/{id}", server.deleteMachineTemplate)
	router.HandleFunc("GET /api/v1/infrastructure-services", server.listInfrastructureServices)
	router.HandleFunc("POST /api/v1/infrastructure-services", server.createInfrastructureService)
	router.HandleFunc("GET /api/v1/infrastructure-services/{id}", server.getInfrastructureService)
	router.HandleFunc("DELETE /api/v1/infrastructure-services/{id}", server.deleteInfrastructureService)
	router.HandleFunc("GET /api/v1/kubernetes-clusters", server.listKubernetesClusters)
	router.HandleFunc("POST /api/v1/kubernetes-clusters", server.createKubernetesCluster)
	router.HandleFunc("GET /api/v1/kubernetes-clusters/{id}", server.getKubernetesCluster)
	router.HandleFunc("DELETE /api/v1/kubernetes-clusters/{id}", server.deleteKubernetesCluster)
	router.HandleFunc("GET /api/v1/kubernetes-clusters/{id}/kubeconfig", server.downloadKubeconfig)
	router.HandleFunc("GET /api/v1/experiment-configurations", server.listExperimentConfigurations)
	router.HandleFunc("POST /api/v1/experiment-configurations", server.createExperimentConfiguration)
	router.HandleFunc("GET /api/v1/experiment-configurations/{id}", server.getExperimentConfiguration)
	router.HandleFunc("POST /api/v1/experiment-configurations/{id}/clone", server.cloneExperimentConfiguration)
	router.HandleFunc("DELETE /api/v1/experiment-configurations/{id}", server.deleteExperimentConfiguration)
	router.HandleFunc("POST /api/v1/experiment-instances", server.createExperimentInstance)
	router.HandleFunc("POST /api/v1/experiment-suites", server.createExperimentSuite)
	router.HandleFunc("GET /api/v1/infrastructure/resources", server.listInfrastructureResources)
	router.HandleFunc("GET /api/v1/terminal-targets", server.listTerminalTargets)
	router.HandleFunc("POST /api/v1/terminals", server.createTerminal)
	router.HandleFunc("GET /api/v1/terminals/{id}/connect", server.connectTerminal)
	router.HandleFunc("GET /api/v1/audit", server.listAuditEvents)
	router.HandleFunc("GET /api/v1/workspaces", server.listWorkspaces)
	router.HandleFunc("POST /api/v1/workspaces", server.createWorkspace)
	router.HandleFunc("GET /api/v1/workspaces/{id}", server.getWorkspace)
	router.HandleFunc("GET /api/v1/pipelines", server.listPipelines)
	router.HandleFunc("POST /api/v1/pipelines", server.createPipeline)
	router.HandleFunc("GET /api/v1/pipelines/{id}", server.getPipeline)
	router.HandleFunc("POST /api/v1/pipelines/{id}/runs", server.createPipelineRun)
	router.HandleFunc("GET /api/v1/pipeline-runs", server.listPipelineRuns)
	router.HandleFunc("GET /api/v1/pipeline-runs/{id}", server.getPipelineRun)
	router.HandleFunc("POST /api/v1/pipeline-runs/{id}/cancel", server.cancelPipelineRun)
	router.HandleFunc("GET /api/v1/experiments", server.listExperiments)
	router.HandleFunc("POST /api/v1/experiments", server.createCompletedExperiment)
	router.HandleFunc("POST /api/v1/experiment-runs", server.createPipelineExperiment)
	router.HandleFunc("GET /api/v1/operations", server.listOperations)
	router.HandleFunc("POST /api/v1/operations", server.createOperation)
	router.HandleFunc("GET /api/v1/operations/{id}", server.getOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/queue", server.queueOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/cancel", server.cancelOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/cleanup", server.createCleanupOperation)
	router.HandleFunc("GET /api/v1/operations/{id}/logs", server.listLogs)
	router.HandleFunc("GET /api/v1/operations/{id}/events", server.operationEvents)
	router.HandleFunc("GET /api/v1/artifacts", server.listAvailableArtifacts)
	router.HandleFunc("GET /api/v1/artifacts/{id}/download", server.downloadArtifact)
	router.Handle("/", webHandler)
	return securityHeaders(requestLog(server.authenticate(auditMutations(store, recoverer(router))))), nil
}

func (s *Server) listPipelines(response http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListPipelines(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list pipelines.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPipeline(response http.ResponseWriter, request *http.Request) {
	pipeline, err := s.store.GetPipeline(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline.")
		return
	}
	writeJSON(response, http.StatusOK, pipeline)
}

func (s *Server) createPipeline(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID string                    `json:"workspaceId"`
		Name        string                    `json:"name"`
		Description string                    `json:"description"`
		Definition  domain.PipelineDefinition `json:"definition"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 120 characters.")
		return
	}
	if len(input.Description) > 500 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_description", "Description cannot exceed 500 characters.")
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	result, err := workflows.Validate(request.Context(), s.registry, input.Definition)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", err.Error())
		return
	}
	if !result.Validation.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": result.Validation})
		return
	}
	for index := range result.Resolution.Stages {
		resolved := &result.Resolution.Stages[index]
		plugin, err := s.registry.Get(resolved.PluginID)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
			return
		}
		manifest := plugin.Manifest()
		if err := validatePlanEffects(manifest, resolved.Plan); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_plan_effects", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		externalPlan := withoutPipelineBindings(resolved.Plan)
		if err := s.validateExternalArtifactInputs(request.Context(), input.WorkspaceID, externalPlan); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_artifact_reference", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		if err := s.validateResourceEffects(request.Context(), input.WorkspaceID, manifest, resolved.Plan); err != nil {
			writeError(response, http.StatusConflict, "resource_conflict", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		if hasPipelineBindings(resolved.Plan) {
			result.Validation.Issues = append(result.Validation.Issues, domain.ValidationIssue{Level: "info", Path: "stages." + resolved.ID, Message: "Runtime preflight will use verified outputs from earlier stages."})
			continue
		}
		preflight := preflightPlan(request.Context(), plugin, resolved.Plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
			return s.resolvePreflightArtifactInputs(ctx, input.WorkspaceID, step)
		})
		result.Validation.Issues = append(result.Validation.Issues, preflight...)
		for _, issue := range preflight {
			if issue.Level == "error" {
				result.Validation.Valid = false
			}
		}
	}
	result.Validation.CheckedAt = time.Now().UTC()
	if !result.Validation.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "preflight_failed", "validation": result.Validation})
		return
	}
	pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
		ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		Definition: result.Definition, Resolution: result.Resolution, Validation: result.Validation, Hash: result.Hash,
	})
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "pipeline_conflict", "A pipeline with this name already exists in the workspace.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated pipeline.")
		return
	}
	writeJSON(response, http.StatusCreated, pipeline)
}

func (s *Server) listPipelineRuns(response http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListPipelineRuns(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list pipeline runs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPipelineRun(response http.ResponseWriter, request *http.Request) {
	run, err := s.store.GetPipelineRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline run.")
		return
	}
	writeJSON(response, http.StatusOK, run)
}

func (s *Server) createPipelineRun(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name         string     `json:"name"`
		ScheduledFor *time.Time `json:"scheduledFor"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Run name must contain between 1 and 120 characters.")
		return
	}
	scheduledFor, err := normalizeScheduledFor(input.ScheduledFor, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", err.Error())
		return
	}
	pipeline, err := s.store.GetPipeline(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline.")
		return
	}
	if !pipeline.Validation.Valid || len(pipeline.Resolution.Stages) != len(pipeline.Definition.Stages) {
		writeError(response, http.StatusConflict, "pipeline_invalid", "The saved pipeline is not executable.")
		return
	}
	for _, stage := range pipeline.Resolution.Stages {
		plugin, err := s.registry.Get(stage.PluginID)
		if err != nil || !plugin.Manifest().Matches(stage.PluginVersion, stage.PluginDigest) {
			writeError(response, http.StatusConflict, "plugin_changed", "An installed plug-in no longer matches the validated pipeline. Create a new pipeline version.")
			return
		}
	}
	run := domain.PipelineRun{
		ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: pipeline.WorkspaceID, Name: input.Name,
		PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version,
		ScheduledFor: &scheduledFor,
	}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{
			ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID,
			PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending,
		})
	}
	created, err := s.store.CreatePipelineRun(request.Context(), run)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "pipeline_changed", "The pipeline changed while the run was being created.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not start pipeline run.")
		return
	}
	writeJSON(response, http.StatusAccepted, created)
}

func (s *Server) cancelPipelineRun(response http.ResponseWriter, request *http.Request) {
	if err := s.store.RequestPipelineRunCancel(request.Context(), request.PathValue("id")); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Pipeline run cannot be canceled in its current state.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not request pipeline cancellation.")
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func hasPipelineBindings(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		for _, input := range step.ArtifactInputs {
			if strings.HasPrefix(input.ArtifactID, "art_pipeline_") {
				return true
			}
		}
	}
	return false
}

func withoutPipelineBindings(plan domain.Plan) domain.Plan {
	copyPlan := plan
	copyPlan.Steps = append([]domain.PlanStep(nil), plan.Steps...)
	for stepIndex := range copyPlan.Steps {
		copyPlan.Steps[stepIndex].ArtifactInputs = append([]domain.ArtifactInput(nil), plan.Steps[stepIndex].ArtifactInputs...)
		for inputIndex := range copyPlan.Steps[stepIndex].ArtifactInputs {
			input := &copyPlan.Steps[stepIndex].ArtifactInputs[inputIndex]
			if strings.HasPrefix(input.ArtifactID, "art_pipeline_") {
				input.ArtifactID = ""
			}
		}
	}
	return copyPlan
}

func (s *Server) live(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	if err := s.store.Ready(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	if err := s.artifacts.Health(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	if state, message := s.runtimeHealth(request.Context()); state == "unavailable" {
		writeError(response, http.StatusServiceUnavailable, "not_ready", "OCI plugin runtime is unavailable: "+message)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) system(response http.ResponseWriter, request *http.Request) {
	stats, err := s.store.Stats(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read platform status.")
		return
	}
	runtimeContext, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	runtimeState, runtimeMessage := s.runtimeHealth(runtimeContext)
	cancel()
	status := "healthy"
	if runtimeState == "unavailable" {
		status = "degraded"
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"name":    "KubePhos",
		"version": s.version,
		"status":  status,
		"features": map[string]bool{
			"ociPluginImport": runtimeState == "healthy",
		},
		"pluginRuntime": map[string]string{"status": runtimeState, "message": runtimeMessage},
		"stats":         stats,
		"checkedAt":     time.Now().UTC(),
	})
}

func (s *Server) runtimeHealth(ctx context.Context) (string, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if s.runtime == nil {
		return "disabled", "External OCI plugins are not enabled."
	}
	if stateful, ok := s.runtime.(plugins.RuntimeState); ok {
		return stateful.State(ctx)
	}
	if err := s.runtime.Ready(ctx); err != nil {
		return "unavailable", err.Error()
	}
	return "healthy", "The dedicated OCI executor is ready."
}

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

func (s *Server) listCatalogApplications(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCatalogApplications(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list catalog applications.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) importCatalogApplication(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Descriptor string `json:"descriptor"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	application, err := catalog.Parse([]byte(input.Descriptor), "imported")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_application", err.Error())
		return
	}
	application, err = s.store.CreateCatalogApplication(request.Context(), application)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "application_conflict", "This application version already exists. Use a new version for changed content.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store application.")
		return
	}
	writeJSON(response, http.StatusCreated, application)
}

func (s *Server) listCredentials(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCredentials(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list credentials.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCredential(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name  string          `json:"name"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,79}$`).MatchString(input.Kind) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Credential kind is invalid.")
		return
	}
	definition, found := s.credentialSchema(input.Kind)
	if !found {
		writeError(response, http.StatusUnprocessableEntity, "unsupported_kind", "No installed plugin declares this credential kind.")
		return
	}
	issues, err := schema.Validate(definition, input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", err.Error())
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": issues})
		return
	}
	nonce, ciphertext, fingerprint, err := s.vault.Encrypt(input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "encryption_failed", "Could not encrypt credential.")
		return
	}
	credential, err := s.store.CreateCredential(request.Context(), domain.EncryptedCredential{
		Credential: domain.Credential{ID: id.New("cred"), Name: input.Name, Kind: input.Kind, Fingerprint: fingerprint},
		Nonce:      nonce, Ciphertext: ciphertext,
	})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "credential_conflict", "A credential with this name and kind already exists.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store credential.")
		return
	}
	writeJSON(response, http.StatusCreated, credential)
}

func (s *Server) credentialSchema(kind string) (json.RawMessage, bool) {
	for _, manifest := range s.registry.Manifests() {
		for _, definition := range manifest.CredentialSchemas {
			if definition.Kind == kind {
				return definition.Schema, true
			}
		}
	}
	return nil, false
}

func (s *Server) listConnections(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProviderConnections(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list provider connections.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createConnection(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name          string          `json:"name"`
		PluginID      string          `json:"pluginId"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	manifest := plugin.Manifest()
	if manifest.Provider == "" || !manifest.HasCapability("infrastructure.discovery") {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", "Select an infrastructure discovery plugin.")
		return
	}
	issues, err := schema.Validate(manifest.Schema, input.Configuration)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", err.Error())
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": issues})
		return
	}
	report := plugin.Validate(request.Context(), input.Configuration)
	if !report.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": report})
		return
	}
	connection, err := s.store.CreateProviderConnection(request.Context(), domain.ProviderConnection{ID: id.New("conn"), Name: input.Name, Provider: manifest.Provider, PluginID: manifest.ID, Configuration: input.Configuration})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "connection_conflict", "A connection with this name already exists for the provider.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store provider connection.")
		return
	}
	writeJSON(response, http.StatusCreated, connection)
}

func (s *Server) deleteConnection(response http.ResponseWriter, request *http.Request) {
	err := s.store.DeleteProviderConnection(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Connection not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "connection_in_use", "Delete the resources associated with this connection first.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete provider connection.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listMachineTemplates(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListManagedResources(request.Context(), "machine-template")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list machine templates.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getMachineTemplate(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "machine-template" {
		writeError(response, http.StatusNotFound, "not_found", "Machine template not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read machine template.")
		return
	}
	writeJSON(response, http.StatusOK, resource)
}

func (s *Server) createMachineTemplate(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID   string          `json:"workspaceId"`
		ConnectionID  string          `json:"connectionId"`
		Name          string          `json:"name"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	if input.Name == "" || len(input.Name) > 32 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Template name must be a lowercase label with at most 32 characters.")
		return
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return
	}
	plugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-template.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	configuration := map[string]any{}
	if len(input.Configuration) > 0 {
		if err := json.Unmarshal(input.Configuration, &configuration); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "Configuration must be a JSON object.")
			return
		}
	}
	configuration["connectionRef"] = connection.ID
	configuration["name"] = input.Name
	spec, err := json.Marshal(configuration)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Configuration could not be encoded.")
		return
	}
	operation, failure := s.validatedManagedOperation(request.Context(), input.WorkspaceID, plugin, "Create VM template "+input.Name, spec)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Status = domain.OperationQueued
	manifest := plugin.Manifest()
	resource, err := s.store.CreateManagedResource(request.Context(), domain.ManagedResource{ID: id.New("tmpl"), WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: "machine-template", Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: spec}, operation)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "template_conflict", "A template with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the machine template workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) deleteMachineTemplate(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "machine-template" {
		writeError(response, http.StatusNotFound, "not_found", "Machine template not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read machine template.")
		return
	}
	if resource.Status == "deletion-failed" {
		if err := s.store.ResetFailedManagedResourceDeletion(request.Context(), resource.ID); err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed template deletion cannot be retried in its current state.")
			return
		}
		resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reload the machine template lifecycle.")
			return
		}
	}
	if resource.Status != "ready" && resource.Status != "failed" && resource.Status != "canceled" {
		writeError(response, http.StatusConflict, "template_not_ready", "Only a completed machine template lifecycle can be deleted.")
		return
	}
	source, err := s.store.GetOperation(request.Context(), resource.OperationID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the template lifecycle.")
		return
	}
	operation, failure := s.validatedManagedCleanup(request.Context(), source)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Status = domain.OperationQueued
	if err := s.store.CreateManagedResourceDeletion(request.Context(), resource.ID, operation); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "template_in_use", "The template is still in use or already being deleted.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the template deletion workflow.")
		return
	}
	resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the template deletion workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) listInfrastructureServices(response http.ResponseWriter, request *http.Request) {
	kind := strings.TrimSpace(request.URL.Query().Get("kind"))
	kinds := []string{"harbor", "nfs"}
	if kind != "" {
		if kind != "harbor" && kind != "nfs" {
			writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Service kind must be harbor or nfs.")
			return
		}
		kinds = []string{kind}
	}
	items := []domain.ManagedResource{}
	for _, current := range kinds {
		resources, err := s.store.ListManagedResources(request.Context(), current)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not list infrastructure services.")
			return
		}
		items = append(items, resources...)
	}
	slices.SortFunc(items, func(left, right domain.ManagedResource) int { return right.CreatedAt.Compare(left.CreatedAt) })
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getInfrastructureService(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "harbor" && resource.Kind != "nfs" {
		writeError(response, http.StatusNotFound, "not_found", "Infrastructure service not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the infrastructure service.")
		return
	}
	writeJSON(response, http.StatusOK, resource)
}

func (s *Server) createInfrastructureService(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID  string `json:"workspaceId"`
		ConnectionID string `json:"connectionId"`
		TemplateID   string `json:"templateId"`
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		VMID         int    `json:"vmid"`
		Address      string `json:"address"`
		PrefixLength int    `json:"prefixLength"`
		Gateway      string `json:"gateway"`
		DNSServer    string `json:"dnsServer"`
		Cores        int    `json:"cores"`
		MemoryMiB    int    `json:"memoryMiB"`
		DiskGiB      int    `json:"diskGiB"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.Kind = strings.TrimSpace(input.Kind)
	input.Name = strings.TrimSpace(input.Name)
	input.Address = strings.TrimSpace(input.Address)
	input.Gateway = strings.TrimSpace(input.Gateway)
	input.DNSServer = strings.TrimSpace(input.DNSServer)
	if input.Kind != "harbor" && input.Kind != "nfs" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Service kind must be harbor or nfs.")
		return
	}
	if input.Name == "" || len(input.Name) > 32 || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Service name must be a lowercase label with at most 32 characters.")
		return
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return
	}
	template, err := s.store.GetManagedResource(request.Context(), input.TemplateID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && template.Kind != "machine-template" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_template", "Select an existing machine template.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the machine template.")
		return
	}
	if template.Status != "ready" || template.ArtifactID == "" {
		writeError(response, http.StatusConflict, "template_not_ready", "The selected machine template is not ready.")
		return
	}
	if template.ConnectionID != connection.ID || template.Provider != connection.Provider || template.WorkspaceID != input.WorkspaceID {
		writeError(response, http.StatusUnprocessableEntity, "template_mismatch", "The template must belong to the selected connection and workspace.")
		return
	}
	var templateSpec struct {
		Node    string `json:"node"`
		VMID    int    `json:"vmid"`
		DiskGiB int    `json:"diskGiB"`
		SSHUser string `json:"sshUser"`
	}
	if err := json.Unmarshal(template.Spec, &templateSpec); err != nil || templateSpec.Node == "" || templateSpec.VMID < 100 || templateSpec.SSHUser == "" {
		writeError(response, http.StatusConflict, "template_invalid", "The saved machine template configuration is incomplete.")
		return
	}
	if input.VMID < 100 || input.VMID > 999999999 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_vmid", "VM ID must be between 100 and 999999999.")
		return
	}
	if input.Cores == 0 {
		input.Cores = map[string]int{"harbor": 4, "nfs": 2}[input.Kind]
	}
	if input.MemoryMiB == 0 {
		input.MemoryMiB = map[string]int{"harbor": 8192, "nfs": 4096}[input.Kind]
	}
	if input.DiskGiB == 0 {
		input.DiskGiB = map[string]int{"harbor": 100, "nfs": 200}[input.Kind]
	}
	if input.Cores < 1 || input.Cores > 32 || input.MemoryMiB < 512 || input.MemoryMiB > 131072 || input.DiskGiB < templateSpec.DiskGiB || input.DiskGiB > 2048 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Capacity is outside the supported range or smaller than the template disk.")
		return
	}
	topologyPlugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-topology.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	serviceCapability := map[string]string{"harbor": "registry.oci.provision", "nfs": "storage.shared.provision"}[input.Kind]
	servicePlugin, err := s.pluginForCapability("", serviceCapability)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	topologySpec, _ := json.Marshal(map[string]any{
		"connectionRef": connection.ID, "machineTemplateRef": template.ArtifactID, "node": templateSpec.Node,
		"templateVMID": templateSpec.VMID, "baseVMID": input.VMID, "namePrefix": input.Name, "machineCount": 1,
		"addressStart": input.Address, "prefixLength": input.PrefixLength, "gateway": input.Gateway, "dnsServer": input.DNSServer,
		"cores": input.Cores, "memoryMiB": input.MemoryMiB, "diskGiB": input.DiskGiB, "sshUser": templateSpec.SSHUser, "cleanupAfterTest": false,
	})
	serviceSpec := map[string]any{"machineSetRef": "", "machineAccessRef": ""}
	if input.Kind == "harbor" {
		serviceSpec["registryName"] = input.Name
	} else {
		serviceSpec["storageName"] = input.Name
	}
	serviceRaw, _ := json.Marshal(serviceSpec)
	resultOutput := map[string]string{"harbor": "registry-endpoint", "nfs": "shared-storage-endpoint"}[input.Kind]
	definition := domain.PipelineDefinition{
		Stages: []domain.PipelineStage{
			{ID: "machine", PluginID: topologyPlugin.Manifest().ID, Title: "Provision dedicated machine", Spec: topologySpec},
			{ID: "service", PluginID: servicePlugin.Manifest().ID, Title: "Install and verify " + input.Kind, Spec: serviceRaw, Bindings: []domain.PipelineBinding{
				{Path: "/machineSetRef", FromStage: "machine", FromOutput: "machine-set"},
				{Path: "/machineAccessRef", FromStage: "machine", FromOutput: "machine-access"},
			}},
		},
		Result: domain.PipelineOutput{Stage: "service", Output: resultOutput},
	}
	validated, failure := s.validatedManagedPipeline(request.Context(), input.WorkspaceID, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	resourceID := id.New("svc")
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: "managed-" + input.Kind + "-" + resourceID, Description: "", Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := domain.PipelineRun{ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: input.WorkspaceID, Name: "Provision " + input.Name, PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending})
	}
	managedSpec, _ := json.Marshal(input)
	manifest := servicePlugin.Manifest()
	resource, err := s.store.CreateManagedPipelineResource(request.Context(), domain.ManagedResource{ID: resourceID, WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: input.Kind, Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: managedSpec}, pipeline, run, []domain.ManagedResourceDependency{{ResourceID: resourceID, DependsOnID: template.ID, Relation: "machine-template"}})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "service_conflict", "A service with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the infrastructure service workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

type managedNodePoolInput struct {
	Name     string                 `json:"name"`
	Count    int                    `json:"count"`
	Zones    []string               `json:"zones"`
	Capacity managedMachineCapacity `json:"capacity"`
}

type managedMachineCapacity struct {
	Cores     int `json:"cores"`
	MemoryMiB int `json:"memoryMiB"`
	DiskGiB   int `json:"diskGiB"`
}

func (s *Server) listKubernetesClusters(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListManagedResources(request.Context(), "kubernetes-cluster")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list Kubernetes clusters.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		slog.Error("read managed cluster", "error", err)
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	writeJSON(response, http.StatusOK, resource)
}

type experimentConfigurationInput struct {
	WorkspaceID       string                                 `json:"workspaceId"`
	ClusterResourceID string                                 `json:"clusterResourceId"`
	Name              string                                 `json:"name"`
	Description       string                                 `json:"description"`
	ApplicationRef    string                                 `json:"applicationRef"`
	Definition        experimentConfigurationDefinitionInput `json:"definition"`
}

type experimentConfigurationDefinitionInput struct {
	ApplicationValues json.RawMessage                         `json:"applicationValues"`
	Components        []experimentConfigurationComponentInput `json:"components"`
}

type experimentConfigurationComponentInput struct {
	ID            string          `json:"id"`
	PluginID      string          `json:"pluginId"`
	PluginVersion string          `json:"pluginVersion,omitempty"`
	PluginDigest  string          `json:"pluginDigest,omitempty"`
	Capability    string          `json:"capability"`
	Configuration json.RawMessage `json:"configuration"`
	Targets       struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"targets"`
}

func (s *Server) listExperimentConfigurations(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListExperimentConfigurations(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list experiment configurations.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	configuration, err := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusOK, configuration)
}

func (s *Server) createExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	var input experimentConfigurationInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	configuration, failure := s.validatedExperimentConfiguration(request.Context(), input)
	if failure != nil {
		failure.write(response)
		return
	}
	created, err := s.store.CreateExperimentConfiguration(request.Context(), configuration)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_conflict", "An experiment configuration with this name already exists in the environment.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) cloneExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	source, err := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	var definition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(source.Definition, &definition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The stored experiment configuration is invalid.")
		return
	}
	configuration, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{WorkspaceID: source.WorkspaceID, ClusterResourceID: source.ClusterResourceID, Name: input.Name, Description: source.Description, ApplicationRef: source.ApplicationRef, Definition: definition})
	if failure != nil {
		failure.write(response)
		return
	}
	created, err := s.store.CreateExperimentConfiguration(request.Context(), configuration)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_conflict", "An experiment configuration with this name already exists in the environment.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not clone the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) deleteExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	err := s.store.DeleteExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete the experiment configuration.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) validatedExperimentConfiguration(ctx context.Context, input experimentConfigurationInput) (domain.ExperimentConfiguration, *managedOperationFailure) {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ClusterResourceID = strings.TrimSpace(input.ClusterResourceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.ApplicationRef = strings.TrimSpace(input.ApplicationRef)
	if input.Name == "" || len([]rune(input.Name)) > 120 || len([]rune(input.Description)) > 1000 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_name", message: "Name is required, with at most 120 characters; description can contain at most 1000 characters."}
	}
	cluster, err := s.store.GetManagedResource(ctx, input.ClusterResourceID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && cluster.Kind != "kubernetes-cluster" {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_cluster", message: "Select a managed Kubernetes cluster."}
	}
	if err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the Kubernetes cluster."}
	}
	if cluster.Status != "ready" || cluster.WorkspaceID != input.WorkspaceID {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_not_ready", message: "The selected cluster must be ready and belong to the same environment."}
	}
	applicationID, applicationVersion, err := catalog.ParseReference(input.ApplicationRef)
	if err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application", message: err.Error()}
	}
	application, err := s.store.GetCatalogApplication(ctx, applicationID, applicationVersion)
	if errors.Is(err, storage.ErrNotFound) {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application", message: "Select an enabled application version from the catalog."}
	}
	if err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the application."}
	}
	if len(input.Definition.ApplicationValues) == 0 {
		input.Definition.ApplicationValues = json.RawMessage(`{}`)
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_application", message: "The selected application contract is invalid."}
	}
	valuesSchema, _ := json.Marshal(descriptor.Spec.ValuesSchema)
	if issues, err := schema.Validate(valuesSchema, input.Definition.ApplicationValues); err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: err.Error()}
	} else if len(issues) > 0 {
		return domain.ExperimentConfiguration{}, experimentConfigurationIssues("applicationValues", issues)
	}
	if len(input.Definition.Components) > 15 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_components", message: "An experiment configuration can contain at most 15 component instances."}
	}
	componentPattern := regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
	applicationComponents := map[string]bool{}
	for _, component := range descriptor.Spec.Interface.Components {
		applicationComponents[component.ID] = true
	}
	seenIDs := map[string]bool{}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	for index := range input.Definition.Components {
		component := &input.Definition.Components[index]
		component.ID = strings.TrimSpace(component.ID)
		component.PluginID = strings.TrimSpace(component.PluginID)
		component.Capability = strings.TrimSpace(component.Capability)
		if !componentPattern.MatchString(component.ID) || seenIDs[component.ID] {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_component", message: "Component instance IDs must be unique lowercase labels."}
		}
		seenIDs[component.ID] = true
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
		}
		manifest := plugin.Manifest()
		component.PluginVersion = manifest.Version
		component.PluginDigest = manifest.Runtime.Digest
		if component.Capability == "" || !manifest.HasCapability(component.Capability) || strings.HasSuffix(component.Capability, ".preflight") || strings.HasSuffix(component.Capability, ".cleanup") || component.Capability == "lifecycle.cleanup" {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_capability", message: "Each component must select a primary capability declared by its plugin."}
		}
		if len(component.Configuration) == 0 {
			component.Configuration = json.RawMessage(`{}`)
		}
		configSchema, err := experimentComponentSchema(manifest.Schema)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: err.Error()}
		}
		if issues, err := schema.Validate(configSchema, component.Configuration); err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: err.Error()}
		} else if len(issues) > 0 {
			return domain.ExperimentConfiguration{}, experimentConfigurationIssues("components."+component.ID+".configuration", issues)
		}
		if !validExperimentTargets(component.Targets.Include, component.Targets.Exclude, componentPattern, applicationComponents) {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_targets", message: "Component targets must contain unique catalog component IDs without overlap."}
		}
		validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "components." + component.ID, Message: manifest.Name + " " + manifest.Version + " is installed and its configurable inputs are valid."})
	}
	validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "clusterResourceId", Message: "Cluster health and every runtime dependency will be checked again before each run step."})
	definition, _ := json.Marshal(input.Definition)
	return domain.ExperimentConfiguration{ID: id.New("expcfg"), WorkspaceID: input.WorkspaceID, ClusterResourceID: input.ClusterResourceID, Name: input.Name, Description: input.Description, ApplicationRef: application.Reference, ApplicationDigest: application.Digest, Definition: definition, Validation: validation}, nil
}

func experimentConfigurationIssues(prefix string, issues []schema.Issue) *managedOperationFailure {
	converted := make([]domain.ValidationIssue, 0, len(issues))
	for _, issue := range issues {
		converted = append(converted, domain.ValidationIssue{Level: "error", Path: prefix + "." + strings.TrimPrefix(issue.Path, "$."), Message: issue.Message})
	}
	return &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": domain.ValidationReport{Valid: false, Issues: converted, CheckedAt: time.Now().UTC()}}}
}

func experimentComponentSchema(raw json.RawMessage) (json.RawMessage, error) {
	var definition map[string]any
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, err
	}
	properties, _ := definition["properties"].(map[string]any)
	filtered := map[string]any{}
	for name, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		if property["format"] == "kubephos-artifact-ref" || property["format"] == "kubephos-application-ref" {
			continue
		}
		filtered[name] = property
	}
	required := []string{}
	rawRequired, _ := definition["required"].([]any)
	for _, rawName := range rawRequired {
		name, _ := rawName.(string)
		if _, exists := filtered[name]; exists {
			required = append(required, name)
		}
	}
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": filtered, "required": required}
	return json.Marshal(result)
}

func validExperimentTargets(include, exclude []string, pattern *regexp.Regexp, allowed map[string]bool) bool {
	seen := map[string]bool{}
	for _, values := range [][]string{include, exclude} {
		for _, value := range values {
			if !pattern.MatchString(value) || seen[value] || !allowed[value] {
				return false
			}
			seen[value] = true
		}
	}
	return true
}

type experimentInstanceInput struct {
	ConfigurationID string     `json:"configurationId"`
	Name            string     `json:"name"`
	Runs            int        `json:"runs"`
	ScheduledFor    *time.Time `json:"scheduledFor"`
}

type experimentArtifactSource struct {
	ExternalID string
	StageID    string
}

func (s *Server) createExperimentInstance(response http.ResponseWriter, request *http.Request) {
	var input experimentInstanceInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.ConfigurationID = strings.TrimSpace(input.ConfigurationID)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 || input.Runs < 1 || input.Runs > 20 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_instance", "Name is required and runs must be between 1 and 20.")
		return
	}
	if input.ScheduledFor != nil {
		value := input.ScheduledFor.UTC()
		if value.Before(time.Now().UTC().Add(-time.Minute)) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", "Scheduled time cannot be in the past.")
			return
		}
		input.ScheduledFor = &value
	}
	configuration, err := s.store.GetExperimentConfiguration(request.Context(), input.ConfigurationID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	var definition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The saved experiment configuration is invalid.")
		return
	}
	for _, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil || !plugin.Manifest().Matches(component.PluginVersion, component.PluginDigest) {
			writeError(response, http.StatusConflict, "plugin_changed", "A configured plugin changed. Clone and validate the configuration again.")
			return
		}
	}
	revalidated, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{
		WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
		Name: configuration.Name, Description: configuration.Description, ApplicationRef: configuration.ApplicationRef, Definition: definition,
	})
	if failure != nil {
		failure.write(response)
		return
	}
	if revalidated.ApplicationDigest != configuration.ApplicationDigest {
		writeError(response, http.StatusConflict, "application_changed", "The application package changed. Clone and validate the configuration again.")
		return
	}
	pipelineDefinition, failure := s.experimentInstancePipeline(request.Context(), configuration, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	validated, failure := s.validatedManagedPipeline(request.Context(), configuration.WorkspaceID, pipelineDefinition)
	if failure != nil {
		failure.write(response)
		return
	}
	instanceID := id.New("exp")
	pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
		ID: id.New("pipe"), WorkspaceID: configuration.WorkspaceID, Name: input.Name + " · " + instanceID,
		Description: "Immutable execution pipeline for " + configuration.Name,
		Definition:  validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated execution pipeline.")
		return
	}
	variantID := id.New("variant")
	experiment := domain.Experiment{
		ID: instanceID, WorkspaceID: configuration.WorkspaceID, ConfigurationID: configuration.ID,
		Kind: "instance",
		Name: input.Name, Description: configuration.Description, Status: domain.OperationQueued,
		ResultType: validated.Resolution.Result.Type, ResultVersion: validated.Resolution.Result.Version, ScheduledFor: input.ScheduledFor,
		Variants: []domain.ExperimentVariant{{ID: variantID, ExperimentID: instanceID, Position: 1, Name: configuration.Name, PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, ConfigurationID: configuration.ID, Configuration: configuration.Definition}},
	}
	runs := map[string]domain.PipelineRun{}
	for position := 1; position <= input.Runs; position++ {
		trialID := id.New("trial")
		run := domain.PipelineRun{
			ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
			Name: fmt.Sprintf("%s · Run %d", input.Name, position), Status: domain.OperationQueued, PipelineHash: pipeline.Hash,
			ResultType: experiment.ResultType, ResultVersion: experiment.ResultVersion, ScheduledFor: input.ScheduledFor,
		}
		for stagePosition, stage := range validated.Definition.Stages {
			run.Stages = append(run.Stages, domain.PipelineRunStage{ID: id.New("runstage"), RunID: run.ID, Position: stagePosition + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending, CleanupStatus: domain.StepPending})
		}
		experiment.Variants[0].Trials = append(experiment.Variants[0].Trials, domain.ExperimentTrial{ID: trialID, VariantID: variantID, Position: position, Status: domain.OperationQueued, PipelineRunID: run.ID})
		runs[trialID] = run
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the experiment instance.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

type experimentSuiteInput struct {
	Name             string     `json:"name"`
	Description      string     `json:"description"`
	ConfigurationIDs []string   `json:"configurationIds"`
	Runs             int        `json:"runs"`
	ScheduledFor     *time.Time `json:"scheduledFor"`
}

type validatedSuiteVariant struct {
	Configuration domain.ExperimentConfiguration
	Definition    experimentConfigurationDefinitionInput
	Pipeline      workflows.Result
}

func (s *Server) createExperimentSuite(response http.ResponseWriter, request *http.Request) {
	var input experimentSuiteInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len([]rune(input.Name)) > 120 || len([]rune(input.Description)) > 1000 || input.Runs < 1 || input.Runs > 20 || len(input.ConfigurationIDs) < 2 || len(input.ConfigurationIDs) > 8 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_suite", "A suite requires a name, two to eight configurations and one to twenty runs per configuration.")
		return
	}
	if input.ScheduledFor != nil {
		value := input.ScheduledFor.UTC()
		if value.Before(time.Now().UTC().Add(-time.Minute)) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", "Scheduled time cannot be in the past.")
			return
		}
		input.ScheduledFor = &value
	}
	seen := map[string]bool{}
	variants := make([]validatedSuiteVariant, 0, len(input.ConfigurationIDs))
	for _, rawID := range input.ConfigurationIDs {
		configurationID := strings.TrimSpace(rawID)
		if configurationID == "" || seen[configurationID] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_suite", "Suite configurations must be unique.")
			return
		}
		seen[configurationID] = true
		configuration, err := s.store.GetExperimentConfiguration(request.Context(), configurationID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_configuration", "A selected experiment configuration is unavailable.")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not read suite configurations.")
			return
		}
		var definition experimentConfigurationDefinitionInput
		if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
			writeError(response, http.StatusConflict, "invalid_snapshot", "A saved experiment configuration is invalid.")
			return
		}
		for _, component := range definition.Components {
			plugin, err := s.registry.Get(component.PluginID)
			if err != nil || !plugin.Manifest().Matches(component.PluginVersion, component.PluginDigest) {
				writeError(response, http.StatusConflict, "plugin_changed", "A configured plugin changed. Clone and validate the configuration again.")
				return
			}
		}
		revalidated, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{
			WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
			Name: configuration.Name, Description: configuration.Description, ApplicationRef: configuration.ApplicationRef, Definition: definition,
		})
		if failure != nil {
			failure.write(response)
			return
		}
		if revalidated.ApplicationDigest != configuration.ApplicationDigest {
			writeError(response, http.StatusConflict, "application_changed", "An application package changed. Clone and validate the configuration again.")
			return
		}
		if len(variants) > 0 {
			first := variants[0].Configuration
			if configuration.WorkspaceID != first.WorkspaceID || configuration.ClusterResourceID != first.ClusterResourceID || configuration.ApplicationRef != first.ApplicationRef {
				writeError(response, http.StatusUnprocessableEntity, "incompatible_suite", "Suite configurations must use the same environment, cluster and application version.")
				return
			}
		}
		pipelineDefinition, failure := s.experimentInstancePipeline(request.Context(), configuration, definition)
		if failure != nil {
			failure.write(response)
			return
		}
		validated, failure := s.validatedManagedPipeline(request.Context(), configuration.WorkspaceID, pipelineDefinition)
		if failure != nil {
			failure.write(response)
			return
		}
		if len(variants) > 0 && (validated.Resolution.Result.Type != variants[0].Pipeline.Resolution.Result.Type || validated.Resolution.Result.Version != variants[0].Pipeline.Resolution.Result.Version) {
			writeError(response, http.StatusUnprocessableEntity, "incompatible_results", "Every suite configuration must produce the same result contract.")
			return
		}
		variants = append(variants, validatedSuiteVariant{Configuration: configuration, Definition: definition, Pipeline: validated})
	}
	suiteID := id.New("exp")
	experiment := domain.Experiment{
		ID: suiteID, WorkspaceID: variants[0].Configuration.WorkspaceID, Kind: "suite", Name: input.Name, Description: input.Description,
		Status: domain.OperationQueued, ResultType: variants[0].Pipeline.Resolution.Result.Type, ResultVersion: variants[0].Pipeline.Resolution.Result.Version, ScheduledFor: input.ScheduledFor,
	}
	runs := map[string]domain.PipelineRun{}
	for variantPosition, item := range variants {
		pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
			ID: id.New("pipe"), WorkspaceID: experiment.WorkspaceID, Name: fmt.Sprintf("%s · Variant %d · %s", input.Name, variantPosition+1, suiteID),
			Description: "Immutable suite pipeline for " + item.Configuration.Name,
			Definition:  item.Pipeline.Definition, Resolution: item.Pipeline.Resolution, Validation: item.Pipeline.Validation, Hash: item.Pipeline.Hash,
		})
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not save a validated suite pipeline.")
			return
		}
		variantID := id.New("variant")
		variant := domain.ExperimentVariant{ID: variantID, ExperimentID: suiteID, Position: variantPosition + 1, Name: item.Configuration.Name, PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, ConfigurationID: item.Configuration.ID, Configuration: item.Configuration.Definition}
		for trialPosition := 1; trialPosition <= input.Runs; trialPosition++ {
			trialID := id.New("trial")
			run := domain.PipelineRun{
				ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: experiment.WorkspaceID, ClusterResourceID: item.Configuration.ClusterResourceID,
				Name: fmt.Sprintf("%s · %s · Run %d", input.Name, item.Configuration.Name, trialPosition), Status: domain.OperationQueued,
				PipelineHash: pipeline.Hash, ResultType: experiment.ResultType, ResultVersion: experiment.ResultVersion, ScheduledFor: input.ScheduledFor,
			}
			for stagePosition, stage := range item.Pipeline.Definition.Stages {
				run.Stages = append(run.Stages, domain.PipelineRunStage{ID: id.New("runstage"), RunID: run.ID, Position: stagePosition + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending, CleanupStatus: domain.StepPending})
			}
			variant.Trials = append(variant.Trials, domain.ExperimentTrial{ID: trialID, VariantID: variantID, Position: trialPosition, Status: domain.OperationQueued, PipelineRunID: run.ID})
			runs[trialID] = run
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the validated experiment suite.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) experimentInstancePipeline(ctx context.Context, configuration domain.ExperimentConfiguration, definition experimentConfigurationDefinitionInput) (domain.PipelineDefinition, *managedOperationFailure) {
	cluster, err := s.store.GetManagedResource(ctx, configuration.ClusterResourceID)
	if err != nil || cluster.Status != "ready" || cluster.PipelineRunID == "" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_not_ready", message: "The configured cluster is not ready."}
	}
	clusterConnection, err := s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
	if err != nil || clusterConnection.Type != "ClusterConnection" || clusterConnection.Version != "v1alpha1" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_invalid", message: "The cluster connection is unavailable."}
	}
	observability, err := s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "observability-capability")
	if err != nil || observability.Type != "ObservabilityCapability" || observability.Version != "v1alpha1" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "observability_invalid", message: "Managed observability is unavailable on the cluster."}
	}
	inspectPlugin, err := s.pluginForCapability("", "applications.inspect")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	deployPlugin, err := s.pluginForCapability("", "applications.kubernetes.deploy")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	bindingPlugin, err := s.pluginForCapability("", "targets.bind")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	applicationValues := definition.ApplicationValues
	if len(applicationValues) == 0 {
		applicationValues = json.RawMessage(`{}`)
	}
	inspectSpec := map[string]any{"applicationRef": configuration.ApplicationRef}
	var values any
	if err := json.Unmarshal(applicationValues, &values); err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_snapshot", message: "Application values are invalid."}
	}
	inspectSpec["values"] = values
	definitionResult := domain.PipelineDefinition{CleanupAfterRun: true}
	definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: "application", PluginID: inspectPlugin.Manifest().ID, Title: "Prepare application package", Spec: marshalRaw(inspectSpec)})
	sources := map[string]experimentArtifactSource{
		artifactContractKey("ClusterConnection", "v1alpha1"):       {ExternalID: clusterConnection.ID},
		artifactContractKey("ObservabilityCapability", "v1alpha1"): {ExternalID: observability.ID},
	}
	registerExperimentOutputs(sources, "application", inspectPlugin.Manifest())
	deploySpec := map[string]any{}
	deployBindings, failure := experimentArtifactBindings(deploySpec, deployPlugin.Manifest(), sources, configuration.ApplicationRef)
	if failure != nil {
		return domain.PipelineDefinition{}, failure
	}
	definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: "deploy", PluginID: deployPlugin.Manifest().ID, Title: "Deploy isolated application", Spec: marshalRaw(deploySpec), Bindings: deployBindings})
	registerExperimentOutputs(sources, "deploy", deployPlugin.Manifest())
	for index, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "plugin_changed", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if manifestNeedsArtifact(manifest, "TargetBinding", "v1alpha1") {
			if manifest.Targeting == nil || manifest.Targeting.RequiredTrait == "" {
				return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_plugin", message: "Plugin " + manifest.ID + " does not declare the required workload trait."}
			}
			bindingID := fmt.Sprintf("targets-%d", index+1)
			bindingSpec := map[string]any{"requiredTrait": manifest.Targeting.RequiredTrait, "includeComponentIds": component.Targets.Include, "excludeComponentIds": component.Targets.Exclude}
			bindings, failure := experimentArtifactBindings(bindingSpec, bindingPlugin.Manifest(), sources, configuration.ApplicationRef)
			if failure != nil {
				return domain.PipelineDefinition{}, failure
			}
			definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: bindingID, PluginID: bindingPlugin.Manifest().ID, Title: "Select targets for " + component.ID, Spec: marshalRaw(bindingSpec), Bindings: bindings})
			registerExperimentOutputs(sources, bindingID, bindingPlugin.Manifest())
		}
		var componentSpec map[string]any
		if err := json.Unmarshal(component.Configuration, &componentSpec); err != nil || componentSpec == nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_snapshot", message: "Component configuration " + component.ID + " is invalid."}
		}
		bindings, failure := experimentArtifactBindings(componentSpec, manifest, sources, configuration.ApplicationRef)
		if failure != nil {
			return domain.PipelineDefinition{}, failure
		}
		stageID := fmt.Sprintf("capability-%d", index+1)
		definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: stageID, PluginID: component.PluginID, Title: component.Capability, Spec: marshalRaw(componentSpec), Bindings: bindings})
		registerExperimentOutputs(sources, stageID, manifest)
		if len(manifest.ArtifactOutputs) == 1 {
			definitionResult.Result = domain.PipelineOutput{Stage: stageID, Type: manifest.ArtifactOutputs[0].Type, Version: manifest.ArtifactOutputs[0].Version}
		}
	}
	if definitionResult.Result.Stage == "" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "missing_result", message: "Add a result-producing capability such as the metrics collector."}
	}
	if len(definitionResult.Stages) > 32 {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "too_many_stages", message: "The configuration expands to more than 32 validated stages."}
	}
	return definitionResult, nil
}

func experimentArtifactBindings(spec map[string]any, manifest plugins.Manifest, sources map[string]experimentArtifactSource, applicationRef string) ([]domain.PipelineBinding, *managedOperationFailure) {
	var schemaDefinition struct {
		Properties map[string]struct {
			Format  string `json:"format"`
			Type    string `json:"x-kubephos-artifact-type"`
			Version string `json:"x-kubephos-artifact-version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(manifest.Schema, &schemaDefinition); err != nil {
		return nil, &managedOperationFailure{status: http.StatusConflict, code: "invalid_plugin", message: "Plugin " + manifest.ID + " has an invalid schema."}
	}
	bindings := []domain.PipelineBinding{}
	for name, property := range schemaDefinition.Properties {
		switch property.Format {
		case "kubephos-application-ref":
			spec[name] = applicationRef
		case "kubephos-artifact-ref":
			source, ok := sources[artifactContractKey(property.Type, property.Version)]
			if !ok {
				return nil, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "missing_input", message: "Plugin " + manifest.ID + " requires unavailable " + property.Type + "/" + property.Version + "."}
			}
			if source.ExternalID != "" {
				spec[name] = source.ExternalID
			} else {
				spec[name] = ""
				bindings = append(bindings, domain.PipelineBinding{Path: "/" + name, FromStage: source.StageID})
			}
		}
	}
	return bindings, nil
}

func registerExperimentOutputs(sources map[string]experimentArtifactSource, stageID string, manifest plugins.Manifest) {
	for _, output := range manifest.ArtifactOutputs {
		sources[artifactContractKey(output.Type, output.Version)] = experimentArtifactSource{StageID: stageID}
	}
}

func manifestNeedsArtifact(manifest plugins.Manifest, artifactType, version string) bool {
	for _, input := range manifest.ArtifactInputs {
		if input.Type == artifactType && input.Version == version {
			return true
		}
	}
	return false
}

func artifactContractKey(artifactType, version string) string {
	return artifactType + "\x00" + version
}

func marshalRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func (s *Server) createKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID          string                 `json:"workspaceId"`
		ConnectionID         string                 `json:"connectionId"`
		TemplateID           string                 `json:"templateId"`
		HarborID             string                 `json:"harborId"`
		NFSID                string                 `json:"nfsId"`
		Name                 string                 `json:"name"`
		BaseVMID             int                    `json:"baseVMID"`
		AddressStart         string                 `json:"addressStart"`
		PrefixLength         int                    `json:"prefixLength"`
		Gateway              string                 `json:"gateway"`
		DNSServer            string                 `json:"dnsServer"`
		ControlPlanes        int                    `json:"controlPlanes"`
		ControlPlaneZones    []string               `json:"controlPlaneZones"`
		ControlPlaneCapacity managedMachineCapacity `json:"controlPlaneCapacity"`
		ManagementPool       managedNodePoolInput   `json:"managementPool"`
		ApplicationPools     []managedNodePoolInput `json:"applicationPools"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.HarborID = strings.TrimSpace(input.HarborID)
	input.NFSID = strings.TrimSpace(input.NFSID)
	input.Name = strings.TrimSpace(input.Name)
	input.AddressStart = strings.TrimSpace(input.AddressStart)
	input.Gateway = strings.TrimSpace(input.Gateway)
	input.DNSServer = strings.TrimSpace(input.DNSServer)
	labelPattern := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	if !labelPattern.MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Cluster name must be a lowercase label with at most 32 characters.")
		return
	}
	if input.ControlPlanes != 1 && input.ControlPlanes != 3 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_control_plane", "Use one or three control plane nodes.")
		return
	}
	if len(input.ControlPlaneZones) == 0 {
		input.ControlPlaneZones = []string{"zone-a"}
	}
	input.ControlPlaneCapacity = managedCapacityDefaults(input.ControlPlaneCapacity, managedMachineCapacity{Cores: 2, MemoryMiB: 4096, DiskGiB: 80})
	if input.ManagementPool.Name == "" {
		input.ManagementPool.Name = "management"
	}
	if input.ManagementPool.Count == 0 {
		input.ManagementPool.Count = 1
	}
	if len(input.ManagementPool.Zones) == 0 {
		input.ManagementPool.Zones = []string{"zone-a"}
	}
	input.ManagementPool.Capacity = managedCapacityDefaults(input.ManagementPool.Capacity, managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80})
	if len(input.ApplicationPools) == 0 {
		input.ApplicationPools = []managedNodePoolInput{{Name: "applications", Count: 2, Zones: []string{"zone-a"}, Capacity: managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80}}}
	}
	seenPools := map[string]bool{input.ManagementPool.Name: true}
	if !validManagedPool(input.ManagementPool, labelPattern) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_pool", "The management pool requires a valid name, node count and at least one zone.")
		return
	}
	totalMachines := input.ControlPlanes + input.ManagementPool.Count
	for index := range input.ApplicationPools {
		pool := &input.ApplicationPools[index]
		pool.Capacity = managedCapacityDefaults(pool.Capacity, managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80})
		if !validManagedPool(*pool, labelPattern) || seenPools[pool.Name] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pool", "Application pools require unique names, a node count and at least one valid zone.")
			return
		}
		seenPools[pool.Name] = true
		totalMachines += pool.Count
	}
	if totalMachines > 12 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "A cluster can contain at most 12 machines in the current provider profile.")
		return
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return
	}
	template, failure := s.managedDependency(request.Context(), input.TemplateID, "machine-template", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return
	}
	harbor, failure := s.managedDependency(request.Context(), input.HarborID, "harbor", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return
	}
	nfs, failure := s.managedDependency(request.Context(), input.NFSID, "nfs", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return
	}
	registryCredential, err := s.store.GetPipelineRunArtifact(request.Context(), harbor.PipelineRunID, "registry-push-credential")
	if err != nil || registryCredential.Type != "RegistryCredential" || !registryCredential.Sensitive {
		writeError(response, http.StatusConflict, "registry_invalid", "The selected Harbor pull identity is unavailable.")
		return
	}
	var templateSpec struct {
		Node    string `json:"node"`
		VMID    int    `json:"vmid"`
		DiskGiB int    `json:"diskGiB"`
		SSHUser string `json:"sshUser"`
	}
	if err := json.Unmarshal(template.Spec, &templateSpec); err != nil || templateSpec.Node == "" || templateSpec.VMID < 100 || templateSpec.SSHUser == "" {
		writeError(response, http.StatusConflict, "template_invalid", "The saved machine template configuration is incomplete.")
		return
	}
	if input.BaseVMID < 100 || input.BaseVMID > 999999988 || input.BaseVMID+totalMachines-1 > 999999999 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_vmid", "The consecutive cluster VM ID range is invalid.")
		return
	}
	if !validManagedCapacity(input.ControlPlaneCapacity, templateSpec.DiskGiB) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Control plane capacity is outside the supported range or smaller than the template disk.")
		return
	}
	if !validManagedCapacity(input.ManagementPool.Capacity, templateSpec.DiskGiB) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Management pool capacity is outside the supported range or smaller than the template disk.")
		return
	}
	for _, pool := range input.ApplicationPools {
		if validManagedCapacity(pool.Capacity, templateSpec.DiskGiB) {
			continue
		}
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Application pool "+pool.Name+" has capacity outside the supported range or smaller than the template disk.")
		return
	}
	topologyPlugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-topology.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	clusterPlugin, err := s.pluginForCapability("", "cluster.bootstrap")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	storagePlugin, err := s.pluginForCapability("", "storage.kubernetes.attach")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	observabilityPlugin, err := s.pluginForCapability("", "observability.metrics.install")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	machineProfiles := make([]managedMachineCapacity, 0, totalMachines)
	machineProfiles = appendManagedMachineProfiles(machineProfiles, input.ControlPlanes, input.ControlPlaneCapacity)
	machineProfiles = appendManagedMachineProfiles(machineProfiles, input.ManagementPool.Count, input.ManagementPool.Capacity)
	for _, pool := range input.ApplicationPools {
		machineProfiles = appendManagedMachineProfiles(machineProfiles, pool.Count, pool.Capacity)
	}
	topologySpec, _ := json.Marshal(map[string]any{"connectionRef": connection.ID, "machineTemplateRef": template.ArtifactID, "node": templateSpec.Node, "templateVMID": templateSpec.VMID, "baseVMID": input.BaseVMID, "addressStart": input.AddressStart, "prefixLength": input.PrefixLength, "gateway": input.Gateway, "dnsServer": input.DNSServer, "namePrefix": input.Name, "machineCount": totalMachines, "cores": input.ControlPlaneCapacity.Cores, "memoryMiB": input.ControlPlaneCapacity.MemoryMiB, "diskGiB": input.ControlPlaneCapacity.DiskGiB, "machineProfiles": machineProfiles, "sshUser": templateSpec.SSHUser, "cleanupAfterTest": false})
	nodePools := []map[string]any{{"name": input.ManagementPool.Name, "role": "management", "count": input.ManagementPool.Count, "zones": input.ManagementPool.Zones}}
	for _, pool := range input.ApplicationPools {
		nodePools = append(nodePools, map[string]any{"name": pool.Name, "role": "application", "count": pool.Count, "zones": pool.Zones})
	}
	clusterSpec, _ := json.Marshal(map[string]any{"machineSetRef": "", "machineAccessRef": "", "clusterName": input.Name, "controlPlanes": input.ControlPlanes, "controlPlaneZones": input.ControlPlaneZones, "nodePools": nodePools, "registryEndpointRef": harbor.ArtifactID, "registryCredentialRef": registryCredential.ID})
	storageSpec, _ := json.Marshal(map[string]any{"clusterConnectionRef": "", "sharedStorageEndpointRef": nfs.ArtifactID, "storageClassName": "shared-storage"})
	observabilitySpec, _ := json.Marshal(map[string]any{"clusterConnectionRef": "", "storageClassCapabilityRef": "", "scrapeInterval": "15s", "retention": "7d"})
	definition := domain.PipelineDefinition{Stages: []domain.PipelineStage{
		{ID: "machines", PluginID: topologyPlugin.Manifest().ID, Title: "Provision cluster machines", Spec: topologySpec},
		{ID: "kubernetes", PluginID: clusterPlugin.Manifest().ID, Title: "Bootstrap Kubernetes", Spec: clusterSpec, Bindings: []domain.PipelineBinding{{Path: "/machineSetRef", FromStage: "machines", FromOutput: "machine-set"}, {Path: "/machineAccessRef", FromStage: "machines", FromOutput: "machine-access"}}},
		{ID: "storage", PluginID: storagePlugin.Manifest().ID, Title: "Attach managed storage", Spec: storageSpec, Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}}},
		{ID: "observability", PluginID: observabilityPlugin.Manifest().ID, Title: "Install managed observability", Spec: observabilitySpec, Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}, {Path: "/storageClassCapabilityRef", FromStage: "storage", FromOutput: "storage-class-capability"}}},
	}, Result: domain.PipelineOutput{Stage: "observability", Output: "observability-capability"}}
	validated, failure := s.validatedManagedPipeline(request.Context(), input.WorkspaceID, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	resourceID := id.New("cluster")
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: "managed-cluster-" + resourceID, Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := pipelineRunForManagedPipeline(pipeline, "Provision "+input.Name)
	managedSpec, _ := json.Marshal(input)
	manifest := clusterPlugin.Manifest()
	dependencies := []domain.ManagedResourceDependency{{ResourceID: resourceID, DependsOnID: template.ID, Relation: "machine-template"}, {ResourceID: resourceID, DependsOnID: harbor.ID, Relation: "registry"}, {ResourceID: resourceID, DependsOnID: nfs.ID, Relation: "shared-storage"}}
	resource, err := s.store.CreateManagedPipelineResource(request.Context(), domain.ManagedResource{ID: resourceID, WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: "kubernetes-cluster", Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: managedSpec}, pipeline, run, dependencies)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "cluster_conflict", "A cluster with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the Kubernetes cluster workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func validManagedPool(pool managedNodePoolInput, pattern *regexp.Regexp) bool {
	if !pattern.MatchString(pool.Name) || pool.Count < 1 || pool.Count > 12 || len(pool.Zones) == 0 || len(pool.Zones) > 12 {
		return false
	}
	seen := map[string]bool{}
	for _, zone := range pool.Zones {
		if !pattern.MatchString(zone) || seen[zone] {
			return false
		}
		seen[zone] = true
	}
	return true
}

func managedCapacityDefaults(capacity, defaults managedMachineCapacity) managedMachineCapacity {
	if capacity.Cores == 0 {
		capacity.Cores = defaults.Cores
	}
	if capacity.MemoryMiB == 0 {
		capacity.MemoryMiB = defaults.MemoryMiB
	}
	if capacity.DiskGiB == 0 {
		capacity.DiskGiB = defaults.DiskGiB
	}
	return capacity
}

func validManagedCapacity(capacity managedMachineCapacity, templateDiskGiB int) bool {
	return capacity.Cores >= 1 && capacity.Cores <= 32 && capacity.MemoryMiB >= 1024 && capacity.MemoryMiB <= 131072 && capacity.DiskGiB >= templateDiskGiB && capacity.DiskGiB <= 2048
}

func appendManagedMachineProfiles(profiles []managedMachineCapacity, count int, capacity managedMachineCapacity) []managedMachineCapacity {
	for range count {
		profiles = append(profiles, capacity)
	}
	return profiles
}

func (s *Server) managedDependency(ctx context.Context, resourceID, kind, workspaceID, connectionID string) (domain.ManagedResource, *managedOperationFailure) {
	resource, err := s.store.GetManagedResource(ctx, resourceID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != kind {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_dependency", message: "Select a ready " + kind + " resource."}
	}
	if err != nil {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the selected dependency."}
	}
	if resource.Status != "ready" || resource.ArtifactID == "" || resource.WorkspaceID != workspaceID || resource.ConnectionID != connectionID {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusConflict, code: "dependency_not_ready", message: "The selected " + kind + " must be ready and belong to the same environment and connection."}
	}
	return resource, nil
}

func pipelineRunForManagedPipeline(pipeline domain.Pipeline, name string) domain.PipelineRun {
	run := domain.PipelineRun{ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: pipeline.WorkspaceID, Name: name, PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending})
	}
	return run
}

func (s *Server) deleteInfrastructureService(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "harbor" && resource.Kind != "nfs" {
		writeError(response, http.StatusNotFound, "not_found", "Infrastructure service not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the infrastructure service.")
		return
	}
	s.deleteManagedPipelineResource(response, request, resource, "service")
}

func (s *Server) deleteKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	used, err := s.store.ClusterHasExperimentConfigurations(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate cluster dependencies.")
		return
	}
	if used {
		writeError(response, http.StatusConflict, "cluster_in_use", "Delete the experiment configurations associated with this cluster first.")
		return
	}
	s.deleteManagedPipelineResource(response, request, resource, "cluster")
}

func (s *Server) deleteManagedPipelineResource(response http.ResponseWriter, request *http.Request, resource domain.ManagedResource, label string) {
	if resource.Status == "deletion-failed" {
		if err := s.store.ResetFailedManagedResourceDeletion(request.Context(), resource.ID); err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed deletion workflow cannot be retried in its current state.")
			return
		}
		var err error
		resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reload the managed lifecycle.")
			return
		}
	}
	if resource.Status != "ready" && resource.Status != "failed" || resource.PipelineRunID == "" {
		writeError(response, http.StatusConflict, label+"_not_ready", "Only a completed managed lifecycle can be deleted.")
		return
	}
	sourceRun, err := s.store.GetPipelineRun(request.Context(), resource.PipelineRunID)
	if err != nil || !terminal(sourceRun.Status) || len(sourceRun.Stages) == 0 {
		writeError(response, http.StatusConflict, "cleanup_unavailable", "The completed managed lifecycle is unavailable.")
		return
	}
	definition := domain.PipelineDefinition{}
	resolution := domain.PipelineResolution{}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	for sourcePosition := len(sourceRun.Stages) - 1; sourcePosition >= 0; sourcePosition-- {
		sourceStage := sourceRun.Stages[sourcePosition]
		if sourceStage.OperationID == "" {
			continue
		}
		source, err := s.store.GetOperation(request.Context(), sourceStage.OperationID)
		if err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "A provisioning stage lifecycle is unavailable.")
			return
		}
		if _, err := cleanupPlan(source); err != nil {
			continue
		}
		cleanup, failure := s.validatedManagedCleanup(request.Context(), source)
		if failure != nil {
			failure.write(response)
			return
		}
		stageID := "cleanup-" + sourceStage.StageID
		definition.Stages = append(definition.Stages, domain.PipelineStage{ID: stageID, PluginID: cleanup.PluginID, Title: "Remove " + strings.ToLower(sourceStage.Title), Spec: cleanup.Spec})
		resolution.Stages = append(resolution.Stages, domain.ResolvedPipelineStage{ID: stageID, PluginID: cleanup.PluginID, PluginVersion: cleanup.PluginVersion, PluginDigest: cleanup.PluginDigest, Spec: cleanup.Spec, Plan: cleanup.Plan})
		validation.Issues = append(validation.Issues, cleanup.Validation.Issues...)
	}
	if len(definition.Stages) == 0 {
		if err := s.store.DeleteFailedManagedResource(request.Context(), resource.ID); errors.Is(err, storage.ErrConflict) {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed lifecycle cannot be dismissed in its current state.")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not dismiss the failed managed lifecycle.")
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	hash, err := workflows.Fingerprint(definition, resolution)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "planning_failed", "Could not fingerprint the deletion workflow.")
		return
	}
	pipelineID := id.New("pipe")
	pipeline := domain.Pipeline{ID: pipelineID, WorkspaceID: resource.WorkspaceID, Name: "delete-managed-" + resource.ID + "-" + strings.TrimPrefix(pipelineID, "pipe_")[:8], Definition: definition, Resolution: resolution, Validation: validation, Hash: hash}
	run := pipelineRunForManagedPipeline(pipeline, "Delete "+resource.Name)
	if err := s.store.CreateManagedPipelineResourceDeletion(request.Context(), resource.ID, pipeline, run); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, label+"_in_use", "The resource is still in use or already being deleted.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the managed deletion workflow.")
		return
	}
	resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the managed deletion workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) downloadKubeconfig(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	if resource.Status != "ready" {
		writeError(response, http.StatusConflict, "cluster_not_ready", "Kubeconfig is available only after every cluster health gate passes.")
		return
	}
	artifact, err := s.store.GetPipelineRunArtifact(request.Context(), resource.PipelineRunID, "cluster-connection")
	if err != nil || artifact.Type != "ClusterConnection" || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
		writeError(response, http.StatusConflict, "kubeconfig_unavailable", "The verified cluster connection is unavailable.")
		return
	}
	raw, err := s.readArtifactPayload(request.Context(), artifact)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "artifact_error", "Could not decrypt the cluster connection.")
		return
	}
	var connection struct {
		Kind string `json:"kind"`
		Spec struct {
			Kubeconfig string `json:"kubeconfig"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &connection); err != nil || connection.Kind != "ClusterConnection" || strings.TrimSpace(connection.Spec.Kubeconfig) == "" {
		writeError(response, http.StatusConflict, "kubeconfig_invalid", "The stored cluster connection is invalid.")
		return
	}
	response.Header().Set("Content-Type", "application/yaml")
	response.Header().Set("Content-Disposition", `attachment; filename="`+resource.Name+`-kubeconfig.yaml"`)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, connection.Spec.Kubeconfig)
}

type managedOperationFailure struct {
	status  int
	code    string
	message string
	payload any
}

func (failure *managedOperationFailure) write(response http.ResponseWriter) {
	if failure.payload != nil {
		writeJSON(response, failure.status, failure.payload)
		return
	}
	writeError(response, failure.status, failure.code, failure.message)
}

func (s *Server) pluginForCapability(provider, capability string) (plugins.Plugin, error) {
	var selected plugins.Plugin
	for _, manifest := range s.registry.Manifests() {
		if provider != "" && manifest.Provider != provider || !manifest.HasCapability(capability) {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("multiple %s implementations are installed for provider %s", capability, provider)
		}
		plugin, err := s.registry.Get(manifest.ID)
		if err != nil {
			return nil, err
		}
		selected = plugin
	}
	if selected == nil {
		return nil, fmt.Errorf("no installed plugin provides %s for provider %s", capability, provider)
	}
	return selected, nil
}

func (s *Server) validatedManagedPipeline(ctx context.Context, workspaceID string, definition domain.PipelineDefinition) (workflows.Result, *managedOperationFailure) {
	if _, err := s.store.GetWorkspace(ctx, workspaceID); errors.Is(err, storage.ErrNotFound) {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_workspace", message: "Select an existing workspace."}
	} else if err != nil {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate workspace."}
	}
	result, err := workflows.Validate(ctx, s.registry, definition)
	if err != nil {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_pipeline", message: err.Error()}
	}
	if !result.Validation.Valid {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": result.Validation}}
	}
	for index := range result.Resolution.Stages {
		resolved := &result.Resolution.Stages[index]
		plugin, err := s.registry.Get(resolved.PluginID)
		if err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if err := validatePlanEffects(manifest, resolved.Plan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		externalPlan := withoutPipelineBindings(resolved.Plan)
		if err := s.validateExternalArtifactInputs(ctx, workspaceID, externalPlan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		if err := s.validateResourceEffects(ctx, workspaceID, manifest, resolved.Plan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		if hasPipelineBindings(resolved.Plan) {
			result.Validation.Issues = append(result.Validation.Issues, domain.ValidationIssue{Level: "info", Path: "stages." + resolved.ID, Message: "Runtime preflight will use verified outputs from earlier stages."})
			continue
		}
		preflight := preflightPlan(ctx, plugin, resolved.Plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
			return s.resolvePreflightArtifactInputs(ctx, workspaceID, step)
		})
		result.Validation.Issues = append(result.Validation.Issues, preflight...)
		for _, issue := range preflight {
			if issue.Level == "error" {
				result.Validation.Valid = false
			}
		}
	}
	result.Validation.CheckedAt = time.Now().UTC()
	if !result.Validation.Valid {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": result.Validation}}
	}
	return result, nil
}

func (s *Server) validatedManagedOperation(ctx context.Context, workspaceID string, plugin plugins.Plugin, title string, spec json.RawMessage) (domain.Operation, *managedOperationFailure) {
	if _, err := s.store.GetWorkspace(ctx, workspaceID); errors.Is(err, storage.ErrNotFound) {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_workspace", message: "Select an existing workspace."}
	} else if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate workspace."}
	}
	manifest := plugin.Manifest()
	issues, err := schema.Validate(manifest.Schema, spec)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: err.Error()}
	}
	if len(issues) > 0 {
		converted := make([]domain.ValidationIssue, 0, len(issues))
		for _, issue := range issues {
			converted = append(converted, domain.ValidationIssue{Level: "error", Path: issue.Path, Message: issue.Message})
		}
		validation := domain.ValidationReport{Valid: false, Issues: converted, CheckedAt: time.Now().UTC()}
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validation}}
	}
	validation := plugin.Validate(ctx, spec)
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validation}}
	}
	plan, err := plugin.Plan(ctx, spec)
	if err != nil || len(plan.Steps) == 0 {
		message := "The plugin produced an empty plan."
		if err != nil {
			message = err.Error()
		}
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "planning_failed", message: message}
	}
	if err := validatePlanEffects(manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: err.Error()}
	}
	if err := artifacts.ValidatePlan(plan, manifest.ArtifactInputs, manifest.ArtifactOutputs); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_graph", message: err.Error()}
	}
	if err := s.validateExternalArtifactInputs(ctx, workspaceID, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: err.Error()}
	}
	if err := s.validateResourceEffects(ctx, workspaceID, manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: err.Error()}
	}
	preflight := preflightPlan(ctx, plugin, plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		return s.resolvePreflightArtifactInputs(ctx, workspaceID, step)
	})
	validation.Issues = append(validation.Issues, preflight...)
	for _, issue := range preflight {
		if issue.Level == "error" {
			validation.Valid = false
		}
	}
	validation.CheckedAt = time.Now().UTC()
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": validation}}
	}
	planHash, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "planning_failed", message: "Could not fingerprint the validated plan."}
	}
	return domain.Operation{ID: id.New("op"), WorkspaceID: workspaceID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Title: title, Status: domain.OperationReady, Spec: spec, Plan: plan, Validation: validation, PlanHash: planHash}, nil
}

func (s *Server) validatedManagedCleanup(ctx context.Context, source domain.Operation) (domain.Operation, *managedOperationFailure) {
	plugin, err := s.registry.Get(source.PluginID)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
	}
	manifest := plugin.Manifest()
	if !terminal(source.Status) || !manifest.Matches(source.PluginVersion, source.PluginDigest) || !manifest.HasCapability("lifecycle.cleanup") {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "cleanup_unavailable", message: "A completed source lifecycle and its exact cleanup plugin are required."}
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "cleanup_unavailable", message: err.Error()}
	}
	if err := validatePlanEffects(manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: err.Error()}
	}
	if err := artifacts.ValidatePlan(plan, manifest.ArtifactInputs, manifest.ArtifactOutputs); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_graph", message: err.Error()}
	}
	if err := s.validateExternalArtifactInputs(ctx, source.WorkspaceID, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: err.Error()}
	}
	if err := s.validateResourceEffects(ctx, source.WorkspaceID, manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: err.Error()}
	}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "info", Message: "Deletion is restricted to the resource created by the original validated lifecycle."}}}
	preflight := preflightPlan(ctx, plugin, plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		return s.resolvePreflightArtifactInputs(ctx, source.WorkspaceID, step)
	})
	validation.Issues = append(validation.Issues, preflight...)
	for _, issue := range preflight {
		if issue.Level == "error" {
			validation.Valid = false
		}
	}
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": validation}}
	}
	spec, _ := json.Marshal(map[string]string{"sourceOperationId": source.ID, "sourcePlanHash": source.PlanHash})
	planHash, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "planning_failed", message: "Could not fingerprint the cleanup plan."}
	}
	return domain.Operation{ID: id.New("op"), WorkspaceID: source.WorkspaceID, PluginID: source.PluginID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Title: "Delete " + source.Title, Status: domain.OperationReady, Spec: spec, Plan: plan, Validation: validation, PlanHash: planHash}, nil
}

func (s *Server) listInfrastructureResources(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListInfrastructureResources(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list infrastructure resources.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listAuditEvents(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListAuditEvents(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list audit events.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listWorkspaces(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListWorkspaces(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list workspaces.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getWorkspace(response http.ResponseWriter, request *http.Request) {
	workspace, err := s.store.GetWorkspace(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Workspace not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read workspace.")
		return
	}
	writeJSON(response, http.StatusOK, workspace)
}

func (s *Server) createWorkspace(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if len(input.Description) > 280 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_description", "Description cannot exceed 280 characters.")
		return
	}
	workspace, err := s.store.CreateWorkspace(request.Context(), domain.Workspace{
		ID:          id.New("ws"),
		Name:        input.Name,
		Description: input.Description,
		Status:      "ready",
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create workspace.")
		return
	}
	writeJSON(response, http.StatusCreated, workspace)
}

type createExperimentInput struct {
	WorkspaceID string                         `json:"workspaceId"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Variants    []createExperimentVariantInput `json:"variants"`
}

type createExperimentVariantInput struct {
	Name             string          `json:"name"`
	Configuration    json.RawMessage `json:"configuration"`
	TrialArtifactIDs []string        `json:"trialArtifactIds"`
}

type createPipelineExperimentInput struct {
	WorkspaceID  string                                 `json:"workspaceId"`
	Name         string                                 `json:"name"`
	Description  string                                 `json:"description"`
	Repetitions  int                                    `json:"repetitions"`
	ScheduledFor *time.Time                             `json:"scheduledFor"`
	Variants     []createPipelineExperimentVariantInput `json:"variants"`
}

type createPipelineExperimentVariantInput struct {
	Name       string `json:"name"`
	PipelineID string `json:"pipelineId"`
}

func (s *Server) listExperiments(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListExperiments(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list experiments.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCompletedExperiment(response http.ResponseWriter, request *http.Request) {
	var input createExperimentInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := normalizeAndValidateExperimentInput(&input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_experiment", err.Error())
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	experiment := domain.Experiment{
		ID: id.New("exp"), WorkspaceID: input.WorkspaceID, Name: input.Name,
		Description: input.Description, Status: domain.OperationSucceeded,
	}
	seenArtifacts := map[string]bool{}
	for variantPosition, requestedVariant := range input.Variants {
		variant := domain.ExperimentVariant{
			ID: id.New("var"), ExperimentID: experiment.ID, Position: variantPosition + 1,
			Name: requestedVariant.Name, Configuration: requestedVariant.Configuration,
		}
		for trialPosition, artifactID := range requestedVariant.TrialArtifactIDs {
			if seenArtifacts[artifactID] {
				writeError(response, http.StatusUnprocessableEntity, "duplicate_trial", "A result artifact can belong to only one trial in an experiment.")
				return
			}
			seenArtifacts[artifactID] = true
			artifact, err := s.store.GetArtifact(request.Context(), artifactID)
			if errors.Is(err, storage.ErrNotFound) {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "A selected result artifact is unavailable.")
				return
			}
			if err != nil {
				writeError(response, http.StatusInternalServerError, "database_error", "Could not validate a result artifact.")
				return
			}
			source, err := s.store.GetOperation(request.Context(), artifact.OperationID)
			if err != nil || source.WorkspaceID != input.WorkspaceID || source.Status != domain.OperationSucceeded || artifact.Sensitive {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "Every trial must use a public result from a successful operation in the same workspace.")
				return
			}
			if err := s.verifyArtifactPayload(request.Context(), artifact); err != nil {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "A selected result failed integrity verification.")
				return
			}
			if experiment.ResultType == "" {
				experiment.ResultType = artifact.Type
				experiment.ResultVersion = artifact.Version
			} else if experiment.ResultType != artifact.Type || experiment.ResultVersion != artifact.Version {
				writeError(response, http.StatusUnprocessableEntity, "incompatible_trials", "All trials must implement the same artifact contract.")
				return
			}
			variant.Trials = append(variant.Trials, domain.ExperimentTrial{
				ID: id.New("trial"), VariantID: variant.ID, Position: trialPosition + 1,
				Status: domain.OperationSucceeded, OperationID: artifact.OperationID, ResultArtifactID: artifact.ID,
			})
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreateCompletedExperiment(request.Context(), experiment)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "experiment_conflict", "Experiment inputs changed during validation. Refresh and try again.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the experiment.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) createPipelineExperiment(response http.ResponseWriter, request *http.Request) {
	var input createPipelineExperimentInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := normalizePipelineExperimentInput(&input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_experiment", err.Error())
		return
	}
	scheduledFor, err := normalizeScheduledFor(input.ScheduledFor, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", err.Error())
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	pipelines := map[string]domain.Pipeline{}
	resultType, resultVersion := "", ""
	for _, requested := range input.Variants {
		pipeline, err := s.store.GetPipeline(request.Context(), requested.PipelineID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", "Every variant must select an existing pipeline.")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not validate a pipeline.")
			return
		}
		if pipeline.WorkspaceID != input.WorkspaceID || !pipeline.Validation.Valid {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", "Every pipeline must be validated and belong to the selected workspace.")
			return
		}
		if pipelineResultSensitive(pipeline) {
			writeError(response, http.StatusUnprocessableEntity, "sensitive_result", "Experiment results must be public artifacts that can be compared and exported.")
			return
		}
		for _, stage := range pipeline.Resolution.Stages {
			plugin, err := s.registry.Get(stage.PluginID)
			if err != nil || !plugin.Manifest().Matches(stage.PluginVersion, stage.PluginDigest) {
				writeError(response, http.StatusConflict, "plugin_changed", "An installed plug-in no longer matches a validated pipeline. Create a new pipeline version.")
				return
			}
		}
		if resultType == "" {
			resultType, resultVersion = pipeline.Resolution.Result.Type, pipeline.Resolution.Result.Version
		} else if resultType != pipeline.Resolution.Result.Type || resultVersion != pipeline.Resolution.Result.Version {
			writeError(response, http.StatusUnprocessableEntity, "incompatible_variants", "Every variant pipeline must produce the same result contract.")
			return
		}
		pipelines[pipeline.ID] = pipeline
	}
	experiment := domain.Experiment{
		ID: id.New("exp"), WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		Status: domain.OperationQueued, ResultType: resultType, ResultVersion: resultVersion,
		ScheduledFor: &scheduledFor,
	}
	runs := map[string]domain.PipelineRun{}
	for variantPosition, requested := range input.Variants {
		pipeline := pipelines[requested.PipelineID]
		variant := domain.ExperimentVariant{
			ID: id.New("var"), ExperimentID: experiment.ID, Position: variantPosition + 1, Name: requested.Name,
			PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, Configuration: json.RawMessage(`{}`),
		}
		for trialPosition := 1; trialPosition <= input.Repetitions; trialPosition++ {
			trial := domain.ExperimentTrial{ID: id.New("trial"), VariantID: variant.ID, Position: trialPosition, Status: domain.OperationQueued}
			run := domain.PipelineRun{
				ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: input.WorkspaceID,
				Name:   fmt.Sprintf("%s · %s · Trial %d", input.Name, requested.Name, trialPosition),
				Status: domain.OperationQueued, PipelineHash: pipeline.Hash,
				ResultType: resultType, ResultVersion: resultVersion,
				ScheduledFor: &scheduledFor,
			}
			for stagePosition, stage := range pipeline.Definition.Stages {
				run.Stages = append(run.Stages, domain.PipelineRunStage{
					ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: stagePosition + 1,
					StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending,
				})
			}
			trial.PipelineRunID = run.ID
			variant.Trials = append(variant.Trials, trial)
			runs[trial.ID] = run
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "experiment_conflict", "A selected pipeline changed during validation. Refresh and try again.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not start the experiment.")
		return
	}
	writeJSON(response, http.StatusAccepted, created)
}

func normalizePipelineExperimentInput(input *createPipelineExperimentInput) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.WorkspaceID == "" {
		return errors.New("workspace is required")
	}
	if input.Name == "" || len(input.Name) > 120 {
		return errors.New("name must contain between 1 and 120 characters")
	}
	if len(input.Description) > 500 {
		return errors.New("description cannot exceed 500 characters")
	}
	if input.Repetitions < 1 || input.Repetitions > 10 {
		return errors.New("repetitions must be between 1 and 10")
	}
	if len(input.Variants) < 2 || len(input.Variants) > 8 || len(input.Variants)*input.Repetitions > 40 {
		return errors.New("use between 2 and 8 variants and at most 40 total trials")
	}
	names, pipelineIDs := map[string]bool{}, map[string]bool{}
	for index := range input.Variants {
		variant := &input.Variants[index]
		variant.Name = strings.TrimSpace(variant.Name)
		variant.PipelineID = strings.TrimSpace(variant.PipelineID)
		if variant.Name == "" || len(variant.Name) > 80 {
			return fmt.Errorf("variant %d name must contain between 1 and 80 characters", index+1)
		}
		if variant.PipelineID == "" {
			return fmt.Errorf("variant %q requires a pipeline", variant.Name)
		}
		nameKey := strings.ToLower(variant.Name)
		if names[nameKey] {
			return errors.New("variant names must be unique")
		}
		if pipelineIDs[variant.PipelineID] {
			return errors.New("each variant must use a different validated pipeline")
		}
		names[nameKey], pipelineIDs[variant.PipelineID] = true, true
	}
	return nil
}

func normalizeScheduledFor(value *time.Time, now time.Time) (time.Time, error) {
	if value == nil {
		return now.UTC(), nil
	}
	scheduled := value.UTC()
	if scheduled.Before(now.Add(-30 * time.Second)) {
		return time.Time{}, errors.New("scheduled time cannot be in the past")
	}
	if scheduled.After(now.AddDate(1, 0, 0)) {
		return time.Time{}, errors.New("scheduled time cannot be more than one year ahead")
	}
	return scheduled, nil
}

func pipelineResultSensitive(pipeline domain.Pipeline) bool {
	for _, stage := range pipeline.Resolution.Stages {
		if stage.ID != pipeline.Definition.Result.Stage {
			continue
		}
		for _, step := range stage.Plan.Steps {
			for _, output := range step.Outputs {
				if output.Name == pipeline.Definition.Result.Output {
					return output.Sensitive
				}
			}
		}
	}
	return true
}

func normalizeAndValidateExperimentInput(input *createExperimentInput) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.WorkspaceID == "" {
		return errors.New("workspace is required")
	}
	if input.Name == "" || len(input.Name) > 120 {
		return errors.New("name must contain between 1 and 120 characters")
	}
	if len(input.Description) > 500 {
		return errors.New("description cannot exceed 500 characters")
	}
	if len(input.Variants) < 2 || len(input.Variants) > 8 {
		return errors.New("an experiment requires between 2 and 8 variants")
	}
	seenNames := map[string]bool{}
	for index := range input.Variants {
		variant := &input.Variants[index]
		variant.Name = strings.TrimSpace(variant.Name)
		if variant.Name == "" || len(variant.Name) > 80 {
			return fmt.Errorf("variant %d name must contain between 1 and 80 characters", index+1)
		}
		key := strings.ToLower(variant.Name)
		if seenNames[key] {
			return errors.New("variant names must be unique")
		}
		seenNames[key] = true
		if len(variant.TrialArtifactIDs) < 1 || len(variant.TrialArtifactIDs) > 20 {
			return fmt.Errorf("variant %q requires between 1 and 20 trials", variant.Name)
		}
		if len(variant.Configuration) == 0 {
			variant.Configuration = json.RawMessage(`{}`)
		}
		var configuration map[string]any
		if err := json.Unmarshal(variant.Configuration, &configuration); err != nil || configuration == nil {
			return fmt.Errorf("variant %q configuration must be a JSON object", variant.Name)
		}
		for trialIndex, artifactID := range variant.TrialArtifactIDs {
			variant.TrialArtifactIDs[trialIndex] = strings.TrimSpace(artifactID)
			if variant.TrialArtifactIDs[trialIndex] == "" {
				return fmt.Errorf("variant %q contains an empty trial artifact", variant.Name)
			}
		}
	}
	return nil
}

func (s *Server) listOperations(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListOperations(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list operations.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getOperation(response http.ResponseWriter, request *http.Request) {
	operation, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation.")
		return
	}
	writeJSON(response, http.StatusOK, operation)
}

func (s *Server) createOperation(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID string          `json:"workspaceId"`
		PluginID    string          `json:"pluginId"`
		Title       string          `json:"title"`
		Spec        json.RawMessage `json:"spec"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_title", "Title must contain between 1 and 120 characters.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	operation, failure := s.validatedManagedOperation(request.Context(), input.WorkspaceID, plugin, input.Title, input.Spec)
	if failure != nil {
		failure.write(response)
		return
	}
	operation, err = s.store.CreateOperation(request.Context(), operation)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated operation.")
		return
	}
	writeJSON(response, http.StatusCreated, operation)
}

func preflightPlan(ctx context.Context, plugin plugins.Plugin, plan domain.Plan, hydrate func(context.Context, domain.PlanStep) (domain.PlanStep, error)) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0, len(plan.Steps))
	priorMutation := false
	for _, step := range plan.Steps {
		path := "plan.steps." + step.ID
		requiresFutureOutput := false
		for _, input := range step.ArtifactInputs {
			requiresFutureOutput = requiresFutureOutput || input.ArtifactID == ""
		}
		if priorMutation || requiresFutureOutput || len(step.ArtifactInputs) > 0 && hydrate == nil {
			issues = append(issues, domain.ValidationIssue{Level: "info", Path: path, Message: "Dynamic preflight will run after prior outputs and mutations are verified."})
			priorMutation = priorMutation || step.Mutating || len(step.Effects) > 0
			continue
		}
		runtimeStep := step
		if len(step.ArtifactInputs) > 0 {
			var err error
			runtimeStep, err = hydrate(ctx, step)
			if err != nil {
				issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Artifact preflight failed: " + err.Error()})
				priorMutation = priorMutation || step.Mutating || len(step.Effects) > 0
				continue
			}
		}
		health, err := plugin.Precheck(ctx, runtimeStep, func(string, string) error { return nil })
		if err != nil {
			issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Preflight failed: " + err.Error()})
			continue
		}
		if health.Status != domain.HealthHealthy {
			issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Preflight is not healthy: " + health.Summary})
			continue
		}
		issues = append(issues, domain.ValidationIssue{Level: "info", Path: path, Message: "Non-mutating preflight passed: " + health.Summary})
		priorMutation = step.Mutating || len(step.Effects) > 0
	}
	return issues
}

func (s *Server) resolvePreflightArtifactInputs(ctx context.Context, workspaceID string, step domain.PlanStep) (domain.PlanStep, error) {
	step.ResolvedInputs = make(map[string]domain.ResolvedArtifact, len(step.ArtifactInputs))
	for _, input := range step.ArtifactInputs {
		if input.ArtifactID == "" {
			return domain.PlanStep{}, fmt.Errorf("input %q requires a future output", input.Name)
		}
		artifact, err := s.store.GetArtifact(ctx, input.ArtifactID)
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("resolve input %q: %w", input.Name, err)
		}
		source, err := s.store.GetOperation(ctx, artifact.OperationID)
		if err != nil || source.WorkspaceID != workspaceID || source.Status != domain.OperationSucceeded {
			return domain.PlanStep{}, fmt.Errorf("input %q does not belong to a successful operation in this workspace", input.Name)
		}
		if artifact.Type != input.Type || artifact.Version != input.Version {
			return domain.PlanStep{}, fmt.Errorf("input %q expected %s/%s, received %s/%s", input.Name, input.Type, input.Version, artifact.Type, artifact.Version)
		}
		value, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("verify input %q: %w", input.Name, err)
		}
		step.ResolvedInputs[input.Name] = domain.ResolvedArtifact{
			ID: artifact.ID, Type: artifact.Type, Version: artifact.Version, MediaType: artifact.MediaType,
			Digest: artifact.Digest, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive, Value: value,
		}
	}
	return step, nil
}

func (s *Server) createCleanupOperation(response http.ResponseWriter, request *http.Request) {
	source, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the source operation.")
		return
	}
	operation, failure := s.validatedManagedCleanup(request.Context(), source)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Title = "Cleanup: " + source.Title
	operation, err = s.store.CreateOperation(request.Context(), operation)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated cleanup operation.")
		return
	}
	writeJSON(response, http.StatusCreated, operation)
}

func cleanupPlan(source domain.Operation) (domain.Plan, error) {
	return workflows.CleanupPlan(source)
}

func (s *Server) validateExternalArtifactInputs(ctx context.Context, workspaceID string, plan domain.Plan) error {
	verified := map[string]domain.Artifact{}
	for _, step := range plan.Steps {
		for _, input := range step.ArtifactInputs {
			if input.ArtifactID == "" {
				continue
			}
			artifact, ok := verified[input.ArtifactID]
			if !ok {
				var err error
				artifact, err = s.store.GetArtifact(ctx, input.ArtifactID)
				if err != nil {
					return fmt.Errorf("step %q input %q references an unavailable artifact", step.ID, input.Name)
				}
				source, err := s.store.GetOperation(ctx, artifact.OperationID)
				if err != nil || source.WorkspaceID != workspaceID || source.Status != domain.OperationSucceeded {
					return fmt.Errorf("step %q input %q must reference a successful operation in the same workspace", step.ID, input.Name)
				}
				if err := s.verifyArtifactPayload(ctx, artifact); err != nil {
					return fmt.Errorf("step %q input %q failed integrity verification: %w", step.ID, input.Name, err)
				}
				verified[input.ArtifactID] = artifact
			}
			if artifact.Type != input.Type || artifact.Version != input.Version {
				return fmt.Errorf("step %q input %q requires %s/%s but artifact %s provides %s/%s", step.ID, input.Name, input.Type, input.Version, input.ArtifactID, artifact.Type, artifact.Version)
			}
		}
	}
	return nil
}

func (s *Server) verifyArtifactPayload(ctx context.Context, artifact domain.Artifact) error {
	_, err := s.readArtifactPayload(ctx, artifact)
	return err
}

func (s *Server) readArtifactPayload(ctx context.Context, artifact domain.Artifact) ([]byte, error) {
	stored, err := s.artifacts.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
	if err != nil {
		return nil, err
	}
	value := stored
	if artifact.Sensitive {
		if s.vault == nil {
			return nil, errors.New("artifact vault is unavailable")
		}
		value, err = s.vault.DecryptBound(artifact.EncryptionNonce, stored, []byte(artifact.ID))
		if err != nil {
			return nil, err
		}
	}
	if err := artifacts.Verify(value, artifact.Digest, artifact.SizeBytes); err != nil {
		return nil, err
	}
	if artifact.MediaType == "application/json" && !json.Valid(value) {
		return nil, errors.New("artifact contains invalid JSON")
	}
	return value, nil
}

func (s *Server) queueOperation(response http.ResponseWriter, request *http.Request) {
	var input struct {
		PlanHash       string `json:"planHash"`
		AcceptWarnings bool   `json:"acceptWarnings"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pending, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation.")
		return
	}
	plugin, err := s.registry.Get(pending.PluginID)
	if err != nil || !plugin.Manifest().Matches(pending.PluginVersion, pending.PluginDigest) {
		writeError(response, http.StatusConflict, "plugin_changed", "The installed plug-in no longer matches the validated operation. Validate it again.")
		return
	}
	operation, err := s.store.QueueOperation(request.Context(), pending.ID, input.PlanHash, input.AcceptWarnings)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue operation.")
		return
	}
	writeJSON(response, http.StatusAccepted, operation)
}

func (s *Server) cancelOperation(response http.ResponseWriter, request *http.Request) {
	if err := s.store.RequestCancel(request.Context(), request.PathValue("id")); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Operation cannot be canceled in its current state.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not request cancellation.")
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func (s *Server) listLogs(response http.ResponseWriter, request *http.Request) {
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	items, err := s.store.ListLogs(request.Context(), request.PathValue("id"), after, 1000)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation logs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) operationEvents(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		logs, err := s.store.ListLogs(request.Context(), request.PathValue("id"), after, 200)
		if err != nil {
			writeSSE(response, "error", map[string]string{"message": "Could not read logs."})
			flusher.Flush()
			return
		}
		for _, entry := range logs {
			writeSSE(response, "log", entry)
			after = entry.Sequence
		}
		operation, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
		if err != nil {
			writeSSE(response, "error", map[string]string{"message": "Operation no longer exists."})
			flusher.Flush()
			return
		}
		writeSSE(response, "status", operation)
		flusher.Flush()
		if terminal(operation.Status) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) listAvailableArtifacts(response http.ResponseWriter, request *http.Request) {
	limit := 200
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 500 {
			limit = parsed
		}
	}
	items, err := s.store.ListAvailableArtifacts(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list verified artifacts.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) downloadArtifact(response http.ResponseWriter, request *http.Request) {
	artifact, err := s.store.GetArtifact(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Artifact not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read artifact metadata.")
		return
	}
	if artifact.Sensitive {
		writeError(response, http.StatusForbidden, "sensitive_artifact", "Sensitive artifacts cannot be downloaded from the browser.")
		return
	}
	value, err := s.readArtifactPayload(request.Context(), artifact)
	if err != nil {
		writeError(response, http.StatusBadGateway, "storage_error", "Artifact integrity verification failed.")
		return
	}
	response.Header().Set("Content-Type", artifact.MediaType)
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifactFilename(artifact)}))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := response.Write(value); err != nil {
		slog.Error("stream artifact", "artifact", artifact.ID, "error", err)
	}
}

func artifactFilename(artifact domain.Artifact) string {
	name := strings.TrimSpace(artifact.Name)
	if filepath.Ext(name) != "" {
		return name
	}
	mediaType := strings.TrimSpace(strings.SplitN(artifact.MediaType, ";", 2)[0])
	extensions, _ := mime.ExtensionsByType(mediaType)
	if len(extensions) > 0 {
		return name + extensions[0]
	}
	return name
}

func resolvedPlanHash(manifest plugins.Manifest, spec json.RawMessage, plan domain.Plan) (string, error) {
	value, err := json.Marshal(struct {
		PluginID      string          `json:"pluginId"`
		PluginVersion string          `json:"pluginVersion"`
		PluginDigest  string          `json:"pluginDigest,omitempty"`
		Spec          json.RawMessage `json:"spec"`
		Plan          domain.Plan     `json:"plan"`
	}{manifest.ID, manifest.Version, manifest.Runtime.Digest, spec, plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func validatePlanEffects(manifest plugins.Manifest, plan domain.Plan) error {
	if plan.PluginID != manifest.ID {
		return fmt.Errorf("plan plugin identity does not match the selected plugin")
	}
	if len(plan.Steps) > 100 {
		return fmt.Errorf("plan exceeds the maximum number of steps")
	}
	stepIDs := map[string]bool{}
	createdResources := map[string]bool{}
	for _, step := range plan.Steps {
		if step.ID == "" || step.Name == "" || stepIDs[step.ID] || !json.Valid(step.Input) {
			return fmt.Errorf("plan contains an invalid or duplicate step identity")
		}
		stepIDs[step.ID] = true
		for _, effect := range step.Effects {
			if manifest.Provider == "" {
				return fmt.Errorf("resource effects require a provider identity")
			}
			if effect.Action != "create" && effect.Action != "delete" {
				return fmt.Errorf("step %q declares unsupported effect %q", step.ID, effect.Action)
			}
			if effect.Action == "create" && !manifest.HasCapability("infrastructure.provision") {
				return fmt.Errorf("plugin is not allowed to provision infrastructure")
			}
			if effect.Action == "delete" && !manifest.HasCapability("infrastructure.deprovision") {
				return fmt.Errorf("plugin is not allowed to deprovision infrastructure")
			}
			if effect.ExternalID == "" || effect.Kind == "" || effect.Name == "" {
				return fmt.Errorf("step %q declares an incomplete resource effect", step.ID)
			}
			if effect.Action == "create" {
				if createdResources[effect.ExternalID] {
					return fmt.Errorf("resource %q is created more than once in the plan", effect.ExternalID)
				}
				createdResources[effect.ExternalID] = true
			}
		}
	}
	return nil
}

func (s *Server) validateResourceEffects(ctx context.Context, workspaceID string, manifest plugins.Manifest, plan domain.Plan) error {
	for position, step := range plan.Steps {
		for _, effect := range step.Effects {
			switch effect.Action {
			case "create":
				if err := s.store.EnsureResourceAvailable(ctx, manifest.Provider, effect.ExternalID); err != nil {
					return err
				}
			case "delete":
				createdInPlan := false
				for _, previous := range plan.Steps[:position] {
					for _, candidate := range previous.Effects {
						if candidate.Action == "create" && candidate.ExternalID == effect.ExternalID {
							createdInPlan = true
						}
					}
				}
				if !createdInPlan {
					if err := s.store.AuthorizeManagedResource(ctx, manifest.Provider, effect.ExternalID, workspaceID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func terminal(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (r *bufferedResponse) Header() http.Header {
	return r.header
}

func (r *bufferedResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *bufferedResponse) Write(value []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(value)
}

func auditMutations(store *storage.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		action, targetType, targetID, audited := auditTarget(request)
		if !audited {
			next.ServeHTTP(response, request)
			return
		}
		buffered := newBufferedResponse()
		next.ServeHTTP(buffered, request)
		if buffered.status == 0 {
			buffered.status = http.StatusOK
		}
		if targetID == "" && buffered.status < 300 {
			var created struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(buffered.body.Bytes(), &created) == nil {
				targetID = created.ID
			}
		}
		outcome := "succeeded"
		if buffered.status >= 500 {
			outcome = "failed"
		} else if buffered.status >= 400 {
			outcome = "rejected"
		}
		details, _ := json.Marshal(map[string]any{"method": request.Method, "path": request.URL.Path, "status": buffered.status})
		if err := store.AppendAuditEvent(request.Context(), domain.AuditEvent{Actor: actorName(request), Action: action, TargetType: targetType, TargetID: targetID, Outcome: outcome, Details: details}); err != nil {
			slog.Error("append audit event", "action", action, "error", err)
		}
		for key, values := range buffered.header {
			response.Header()[key] = values
		}
		response.WriteHeader(buffered.status)
		_, _ = response.Write(buffered.body.Bytes())
	})
}

func auditTarget(request *http.Request) (string, string, string, bool) {
	if request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodPatch && request.Method != http.MethodDelete {
		return "", "", "", false
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		switch parts[0] {
		case "workspaces":
			return "workspace.create", "workspace", "", true
		case "credentials":
			return "credential.create", "credential", "", true
		case "connections":
			return "connection.create", "connection", "", true
		case "operations":
			return "operation.create", "operation", "", true
		case "pipelines":
			return "pipeline.create", "pipeline", "", true
		case "experiments":
			return "experiment.create", "experiment", "", true
		case "experiment-runs":
			return "experiment.run.create", "experiment", "", true
		case "plugins":
			return "plugin.import", "plugin", "", true
		case "terminals":
			return "terminal.ticket.create", "ssh-terminal", "", true
		}
	}
	if len(parts) == 3 && parts[0] == "pipelines" && parts[2] == "runs" {
		return "pipeline.run.create", "pipeline", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "plugins" && parts[1] == "inspect" {
		return "plugin.inspect", "plugin", "", true
	}
	if len(parts) == 3 && parts[0] == "plugin-packages" && parts[2] == "activate" {
		return "plugin.activate", "plugin-package", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "plugin-packages" && parts[2] == "deactivate" {
		return "plugin.deactivate", "plugin-package", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "plugin-runtime" {
		switch parts[1] {
		case "validate":
			return "plugin-runtime.validate", "plugin-runtime", "default", true
		case "activate":
			return "plugin-runtime.activate", "plugin-runtime", "default", true
		case "deactivate":
			return "plugin-runtime.deactivate", "plugin-runtime", "default", true
		}
	}
	if len(parts) == 3 && parts[0] == "pipeline-runs" && parts[2] == "cancel" {
		return "pipeline.run.cancel", "pipeline-run", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "catalog" && parts[1] == "applications" {
		return "application.import", "catalog-application", "", true
	}
	if len(parts) == 3 && parts[0] == "operations" {
		switch parts[2] {
		case "queue":
			return "operation.queue", "operation", parts[1], true
		case "cancel":
			return "operation.cancel", "operation", parts[1], true
		case "cleanup":
			return "operation.cleanup.create", "operation", parts[1], true
		}
	}
	return request.Method + " " + path, "api", "", true
}

func decodeJSON(request *http.Request, target any) error {
	reader := io.LimitReader(request.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func writeSSE(response io.Writer, event string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, payload)
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		if strings.HasPrefix(request.URL.Path, "/api/") {
			slog.Info("request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
		}
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("request panic", "error", recovered)
				writeError(response, http.StatusInternalServerError, "internal_error", "Unexpected server error.")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func Serve(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	result := make(chan error, 1)
	go func() {
		result <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
