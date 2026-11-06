package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/workflows"
	applicationdeployer "kubephos.dev/kubephos/plugins/application-deployer/runtime"
)

func (s *Server) ownedApplicationDeployment(w http.ResponseWriter, r *http.Request) (domain.ManagedResource, applicationDeploymentInput, bool) {
	resource, err := s.store.GetManagedResource(r.Context(), r.PathValue("id"))
	var input applicationDeploymentInput
	if err != nil || resource.Kind != "application-deployment" || json.Unmarshal(resource.Spec, &input) != nil || !ownsResource(requestUser(r), input.OwnerUserID) {
		writeError(w, 404, "not_found", "Application deployment not found.")
		return resource, input, false
	}
	if !s.validateClusterAssignment(w, r, requestUser(r), input.ClusterResourceID) {
		return resource, input, false
	}
	return resource, input, true
}

func (s *Server) clusterKubeconfigs(ctx context.Context, cluster domain.ManagedResource) ([]string, error) {
	stages := []string{""}
	if group, multi := multiClusterSpec(cluster.Spec); multi {
		stages = nil
		for i := range group.Members {
			stages = append(stages, fmt.Sprintf("c%d-kubernetes", i+1))
		}
	}
	result := []string{}
	for _, stage := range stages {
		var artifact domain.Artifact
		var err error
		if stage == "" {
			artifact, err = s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
		} else {
			artifact, err = s.store.GetPipelineRunArtifactFromStage(ctx, cluster.PipelineRunID, stage, "cluster-connection")
		}
		if err != nil || artifact.Type != "ClusterConnection" || artifact.Version != "v1alpha1" || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
			return nil, errors.New("Verified cluster access is unavailable.")
		}
		raw, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			return nil, errors.New("Verified cluster access could not be read.")
		}
		var connection struct {
			Spec struct {
				Kubeconfig string `json:"kubeconfig"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &connection) != nil || connection.Spec.Kubeconfig == "" {
			return nil, errors.New("Verified cluster access is invalid.")
		}
		result = append(result, connection.Spec.Kubeconfig)
	}
	return result, nil
}

func (s *Server) updateApplicationDeploymentPlacement(w http.ResponseWriter, r *http.Request) {
	resource, saved, ok := s.ownedApplicationDeployment(w, r)
	if !ok {
		return
	}
	var input struct {
		UpdatedAt time.Time                      `json:"updatedAt"`
		Placement *placement.ApplicationSettings `json:"applicationPlacement"`
	}
	if decodeJSON(r, &input) != nil || input.UpdatedAt.IsZero() {
		writeError(w, 422, "invalid_settings", "Refresh the deployment and retry.")
		return
	}
	if resource.Status != "ready" && resource.Status != "provisioning" && resource.Status != "failed" {
		writeError(w, 409, "deployment_busy", "Placement cannot be edited during removal.")
		return
	}
	if err := placement.ValidateApplication(input.Placement); err != nil {
		writeError(w, 422, "invalid_placement", err.Error())
		return
	}
	preview, failure := s.deploymentPlacementPreview(r.Context(), resource, saved)
	if failure != nil {
		failure.write(w)
		return
	}
	targets, _ := json.Marshal(preview.WorkloadTargets)
	if err := applicationdeployer.ValidatePlacement(preview.Content, targets, input.Placement); err != nil {
		writeError(w, 422, "invalid_placement", err.Error())
		return
	}
	cluster, err := s.store.GetManagedResource(r.Context(), saved.ClusterResourceID)
	if err != nil {
		writeError(w, 409, "cluster_unavailable", "The cluster is unavailable.")
		return
	}
	previous := saved.Definition.ApplicationPlacement
	saved.Definition.ApplicationPlacement = input.Placement
	saved.PlacementControlName = "execution"
	controlName := strings.ReplaceAll("kubephos-placement-"+resource.PipelineRunID, "_", "-")
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var applyError error
	err = s.store.UpdateApplicationDeploymentSettings(ctx, resource.ID, saved.ClusterResourceID, input.UpdatedAt, marshalRaw(saved), func() error {
		if resource.Status == "failed" {
			return nil
		}
		configs, err := s.clusterKubeconfigs(ctx, cluster)
		if err != nil {
			applyError = err
			return nil
		}
		control := applicationdeployer.PlacementControl{ControlName: controlName, Settings: input.Placement, Previous: previous}
		for _, config := range configs {
			if err = applicationdeployer.WritePlacementControl(ctx, config, controlName, control); err == nil {
				err = applicationdeployer.ApplyLivePlacement(ctx, config, saved.Definition.Namespace, strings.ReplaceAll(resource.PipelineRunID, "_", "-"), preview.Content, targets, control)
			}
			if err != nil {
				applyError = err
				return nil
			}
		}
		return nil
	})
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "deployment_changed", "The deployment has changed. Refresh and retry.")
		return
	}
	if err != nil {
		if applyError != nil {
			writeError(w, 502, "placement_failed", "Could not apply placement. Check cluster connectivity and retry.")
		} else {
			writeError(w, 500, "database_error", "Could not save placement.")
		}
		return
	}
	updated, err := s.store.GetManagedResource(r.Context(), resource.ID)
	if err != nil {
		writeError(w, 500, "database_error", "Placement was saved. Refresh the deployment.")
		return
	}
	if applyError != nil {
		body := map[string]any{}
		raw, _ := json.Marshal(updated)
		_ = json.Unmarshal(raw, &body)
		body["placementWarning"] = "Placement saved. The cluster update is pending; save again to retry."
		writeJSON(w, http.StatusAccepted, body)
		return
	}
	writeJSON(w, 200, updated)
}

func (s *Server) retryApplicationDeployment(w http.ResponseWriter, r *http.Request) {
	resource, input, ok := s.ownedApplicationDeployment(w, r)
	if !ok {
		return
	}
	if resource.Status != "failed" {
		writeError(w, 409, "deployment_not_failed", "Only a failed deployment can be retried.")
		return
	}
	cluster, err := s.store.GetManagedResource(r.Context(), input.ClusterResourceID)
	if err != nil || cluster.Status != "ready" {
		writeError(w, 409, "cluster_not_ready", "Select a ready Kubernetes cluster.")
		return
	}
	sourceRun, err := s.store.GetPipelineRun(r.Context(), resource.PipelineRunID)
	if err != nil {
		writeError(w, 409, "workflow_unavailable", "The deployment lifecycle is unavailable.")
		return
	}
	sourcePipeline, err := s.store.GetPipeline(r.Context(), resource.PipelineID)
	if err != nil {
		writeError(w, 409, "workflow_unavailable", "The deployment workflow is unavailable.")
		return
	}
	targets, err := clusterRecreationCleanupTargets(sourceRun, sourcePipeline.Definition)
	if err != nil {
		writeError(w, 409, "cleanup_unavailable", err.Error())
		return
	}
	input.PlacementControlName = "execution"
	definition, failure := s.applicationDeploymentPipeline(r.Context(), input, cluster)
	if failure != nil {
		failure.write(w)
		return
	}
	cleanupStages := []domain.PipelineStage{}
	cleanupResolution := []domain.ResolvedPipelineStage{}
	for _, target := range targets {
		operation, err := s.store.GetOperation(r.Context(), target.OperationID)
		if err != nil {
			writeError(w, 409, "cleanup_unavailable", "The original cleanup target is unavailable.")
			return
		}
		if _, err = cleanupPlan(operation); errors.Is(err, workflows.ErrNoCleanupSteps) {
			continue
		}
		cleanup, failure := s.validatedManagedCleanup(r.Context(), operation)
		if failure != nil {
			failure.write(w)
			return
		}
		cleanupStages = append(cleanupStages, domain.PipelineStage{ID: target.StageID, Title: target.Title, PluginID: cleanup.PluginID, Spec: cleanup.Spec})
		cleanupResolution = append(cleanupResolution, domain.ResolvedPipelineStage{ID: target.StageID, PluginID: cleanup.PluginID, PluginVersion: cleanup.PluginVersion, PluginDigest: cleanup.PluginDigest, Spec: cleanup.Spec, Plan: cleanup.Plan})
	}
	validated, failure := s.validatedManagedPipeline(r.Context(), resource.WorkspaceID, definition)
	if failure != nil {
		failure.write(w)
		return
	}
	validated.Definition.Stages = append(cleanupStages, validated.Definition.Stages...)
	validated.Resolution.Stages = append(cleanupResolution, validated.Resolution.Stages...)
	validated.Hash, err = workflows.Fingerprint(validated.Definition, validated.Resolution)
	if err != nil {
		writeError(w, 500, "planning_failed", "Could not prepare the deployment retry.")
		return
	}
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: resource.WorkspaceID, Name: "Retry " + resource.Name + " · " + id.New("retry"), Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := pipelineRunForManagedPipeline(pipeline, "Retry "+resource.Name)
	run.ClusterResourceID = input.ClusterResourceID
	run.OwnerUserID = input.OwnerUserID
	run.IsolationMode = "namespaced"
	run.ApplicationNamespace = input.Definition.Namespace
	if err = s.store.RetryApplicationDeployment(r.Context(), resource.ID, input.ClusterResourceID, resource.UpdatedAt, marshalRaw(input), pipeline, run); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, 409, "deployment_changed", "The deployment has changed. Refresh and retry.")
		} else {
			writeError(w, 500, "retry_failed", "Could not queue the deployment retry.")
		}
		return
	}
	updated, err := s.store.GetManagedResource(r.Context(), resource.ID)
	if err != nil {
		writeError(w, 500, "database_error", "Retry was queued. Refresh the deployment.")
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}

func (s *Server) getIstioProxyDefaults(w http.ResponseWriter, r *http.Request) {
	if !s.validateClusterAssignment(w, r, requestUser(r), r.PathValue("id")) {
		return
	}
	cluster, err := s.store.GetManagedResource(r.Context(), r.PathValue("id"))
	if err != nil || cluster.Kind != "kubernetes-cluster" {
		writeError(w, 404, "not_found", "Kubernetes cluster not found.")
		return
	}
	if _, multi := multiClusterSpec(cluster.Spec); multi {
		writeJSON(w, 200, map[string]any{"available": false})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	configs, err := s.clusterKubeconfigs(ctx, cluster)
	if err != nil || len(configs) != 1 {
		writeError(w, 409, "defaults_unavailable", "Istio defaults are unavailable for this cluster.")
		return
	}
	resources, err := applicationdeployer.IstioProxyDefaults(ctx, configs[0])
	if err != nil {
		writeError(w, 502, "defaults_unavailable", "Could not read Istio proxy defaults from the cluster.")
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=30")
	writeJSON(w, 200, map[string]any{"available": true, "resources": resources})
}

// Live changes use the immutable package actually deployed, even if the catalog
// application has since changed. Before materialization finishes, use its preview.
func (s *Server) deploymentPlacementPreview(ctx context.Context, resource domain.ManagedResource, input applicationDeploymentInput) (applicationPreview, *managedOperationFailure) {
	manifest, manifestErr := s.store.GetPipelineRunArtifactFromStage(ctx, resource.PipelineRunID, "application", "manifest-set")
	workloads, workloadsErr := s.store.GetPipelineRunArtifactFromStage(ctx, resource.PipelineRunID, "application", "workload-targets")
	if manifestErr == nil && workloadsErr == nil {
		failure := &managedOperationFailure{status: 409, code: "package_unavailable", message: "The verified deployment package is unavailable."}
		if manifest.Type != "ManifestSet" || manifest.Version != "v1alpha1" || workloads.Type != "WorkloadTargets" || workloads.Version != "v1alpha1" || manifest.VerifiedAt.IsZero() || workloads.VerifiedAt.IsZero() {
			return applicationPreview{}, failure
		}
		raw, err := s.readArtifactPayload(ctx, manifest)
		if err != nil {
			return applicationPreview{}, failure
		}
		var value struct {
			Spec struct {
				Content string `json:"content"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &value) != nil || value.Spec.Content == "" {
			return applicationPreview{}, failure
		}
		raw, err = s.readArtifactPayload(ctx, workloads)
		if err != nil {
			return applicationPreview{}, failure
		}
		preview := applicationPreview{Content: value.Spec.Content}
		if json.Unmarshal(raw, &preview.WorkloadTargets) != nil || len(preview.WorkloadTargets) == 0 {
			return applicationPreview{}, failure
		}
		return preview, nil
	}
	if manifestErr == nil || workloadsErr == nil || !errors.Is(manifestErr, storage.ErrNotFound) || !errors.Is(workloadsErr, storage.ErrNotFound) {
		return applicationPreview{}, &managedOperationFailure{status: 409, code: "package_unavailable", message: "The verified deployment package is unavailable."}
	}
	applicationID, err := catalog.ParseReference(input.ApplicationRef)
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: 409, code: "application_unavailable", message: "The saved application is unavailable."}
	}
	app, err := s.store.GetCatalogApplication(ctx, applicationID)
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: 409, code: "application_unavailable", message: "The saved application is unavailable."}
	}
	var values map[string]any
	_ = json.Unmarshal(input.Definition.ApplicationValues, &values)
	return s.buildApplicationPreviewWithValues(ctx, app, values)
}
func (s *Server) getDeploymentPlacementDefaults(w http.ResponseWriter, r *http.Request) {
	resource, input, ok := s.ownedApplicationDeployment(w, r)
	if !ok {
		return
	}
	preview, failure := s.deploymentPlacementPreview(r.Context(), resource, input)
	if failure != nil {
		failure.write(w)
		return
	}
	targets, _ := json.Marshal(preview.WorkloadTargets)
	defaults, err := applicationdeployer.PlacementDefaults(preview.Content, targets)
	if err != nil {
		writeError(w, 409, "defaults_unavailable", "Application placement defaults are unavailable.")
		return
	}
	writeJSON(w, 200, map[string]any{"workloadTargets": preview.WorkloadTargets, "placementDefaults": defaults})
}
