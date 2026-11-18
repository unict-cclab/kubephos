package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/workflows"
)

var ownershipMarkerPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type clusterComponentChangeInput struct {
	Components []string `json:"components"`
}

// changeClusterComponents reconciles the platform components of a ready single cluster.
// Machines, Kubernetes, storage and observability are reused; only the platform stage
// runs again, with the ownership marker of the installed components.
func (s *Server) changeClusterComponents(w http.ResponseWriter, r *http.Request) {
	resource, err := s.store.GetManagedResource(r.Context(), r.PathValue("id"))
	if err != nil || resource.Kind != "kubernetes-cluster" {
		writeError(w, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if resource.Status != "ready" {
		writeError(w, http.StatusConflict, "cluster_not_ready", "Only a ready cluster can change its components.")
		return
	}
	var input clusterComponentChangeInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var saved managedKubernetesClusterInput
	if json.Unmarshal(resource.Spec, &saved) != nil {
		writeError(w, http.StatusConflict, "configuration_invalid", "Saved cluster configuration is invalid.")
		return
	}
	if saved.Mode == "multi" {
		writeError(w, http.StatusConflict, "multi_cluster_components_unavailable", "Components cannot be changed on multi cluster environments yet.")
		return
	}
	if input.Components == nil {
		input.Components = []string{}
	}
	components, err := managedcomponents.ResolveClusterComponents(input.Components, saved.Networking)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_components", err.Error())
		return
	}
	if slices.Equal(components, managedcomponents.SavedClusterComponents(saved.Components, saved.Networking)) {
		writeError(w, http.StatusUnprocessableEntity, "components_unchanged", "The cluster already runs these components.")
		return
	}
	desired := saved
	desired.Components = components
	desiredSpec, _ := json.Marshal(desired)
	if failure := s.componentsStillUsed(r, domain.ManagedResource{ID: resource.ID, Name: resource.Name, Spec: desiredSpec}); failure != nil {
		failure.write(w)
		return
	}
	marker, failure := s.platformOwnershipMarker(r, resource)
	if failure != nil {
		failure.write(w)
		return
	}
	source, err := s.store.GetPipeline(r.Context(), resource.PipelineID)
	if err != nil {
		writeError(w, http.StatusConflict, "workflow_unavailable", "Cluster workflow is unavailable.")
		return
	}
	definition := clusterProvisioningStages(source.Definition)
	for index := range definition.Stages {
		stage := &definition.Stages[index]
		if stage.ID != "kubernetes" && stage.ID != "platform" {
			continue
		}
		var spec map[string]any
		if json.Unmarshal(stage.Spec, &spec) != nil || spec == nil {
			writeError(w, http.StatusConflict, "workflow_unavailable", "Cluster stage configuration is invalid.")
			return
		}
		spec["components"] = components
		if stage.ID == "platform" {
			spec["reconcile"], spec["ownershipMarker"] = true, marker
			stage.Title = "Reconcile cluster components"
		}
		stage.Spec, _ = json.Marshal(spec)
	}
	pipeline, failure := s.componentChangePipeline(r, resource, definition)
	if failure != nil {
		failure.write(w)
		return
	}
	lifecycle := pipelineRunForManagedPipeline(pipeline, "Maintain "+resource.Name)
	if err := s.store.CreateManagedClusterComponentChange(r.Context(), resource.ID, resource.PipelineRunID, desiredSpec, pipeline, lifecycle); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, http.StatusConflict, "cluster_in_use", "Changing components requires no queued or running experiments or other lifecycle operations.")
		} else {
			writeError(w, http.StatusInternalServerError, "database_error", "Could not queue the component change.")
		}
		return
	}
	updated, err := s.store.GetManagedResource(r.Context(), resource.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not read the component change.")
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}

// componentChangePipeline plans the cluster workflow without prechecking the reused
// stages, whose machines and services already exist, and prechecks only the platform
// stage against the running cluster, with the outputs of the completed stages.
func (s *Server) componentChangePipeline(r *http.Request, resource domain.ManagedResource, definition domain.PipelineDefinition) (domain.Pipeline, *managedOperationFailure) {
	unavailable := &managedOperationFailure{status: http.StatusConflict, code: "workflow_unavailable", message: "The completed cluster stages no longer provide the platform inputs."}
	validated, err := workflows.ValidateDeferred(r.Context(), s.registry, definition)
	if err != nil {
		return domain.Pipeline{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_pipeline", message: err.Error()}
	}
	if !validated.Validation.Valid {
		return domain.Pipeline{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validated.Validation}}
	}
	platform := slices.IndexFunc(validated.Definition.Stages, func(stage domain.PipelineStage) bool { return stage.ID == "platform" })
	run, err := s.store.GetPipelineRun(r.Context(), resource.PipelineRunID)
	if platform < 0 || err != nil {
		return domain.Pipeline{}, unavailable
	}
	reused := slices.DeleteFunc(slices.Clone(run.Stages), func(stage domain.PipelineRunStage) bool {
		return stage.StageID == "platform" || !slices.ContainsFunc(validated.Definition.Stages, func(provisioning domain.PipelineStage) bool { return provisioning.ID == stage.StageID })
	})
	produced, err := s.completedStageOutputs(r.Context(), resource.WorkspaceID, reused)
	if err != nil {
		return domain.Pipeline{}, unavailable
	}
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: resource.WorkspaceID, Name: fmt.Sprintf("components-%s-%s", resource.ID, id.New("change")), Definition: validated.Definition, Resolution: validated.Resolution}
	resolved, err := workflows.ResolveStage(pipeline, platform, produced)
	if err != nil {
		return domain.Pipeline{}, unavailable
	}
	stage := validated.Definition.Stages[platform]
	checked, failure := s.validatedManagedPipeline(r.Context(), resource.WorkspaceID, domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Spec: resolved.Spec}}, Result: domain.PipelineOutput{Stage: stage.ID, Type: "ManagedPlatformCapability"}})
	if failure != nil {
		return domain.Pipeline{}, failure
	}
	pipeline.Validation = checked.Validation
	if pipeline.Hash, err = workflows.Fingerprint(pipeline.Definition, pipeline.Resolution); err != nil {
		return domain.Pipeline{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "planning_failed", message: "Could not fingerprint the component change."}
	}
	return pipeline, nil
}

// componentsStillUsed rejects a change that removes components saved configurations need.
func (s *Server) componentsStillUsed(r *http.Request, desired domain.ManagedResource) *managedOperationFailure {
	configurations, err := s.store.ListExperimentConfigurations(r.Context())
	if err != nil {
		return &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the experiment configurations of the cluster."}
	}
	used := []string{}
	for _, configuration := range configurations {
		var definition experimentConfigurationDefinitionInput
		if configuration.ClusterResourceID != desired.ID || json.Unmarshal(configuration.Definition, &definition) != nil {
			continue
		}
		if s.validateClusterComponents(desired, definition) != nil {
			used = append(used, configuration.Name)
		}
	}
	if len(used) == 0 {
		return nil
	}
	return &managedOperationFailure{status: http.StatusConflict, code: "components_in_use", message: "These experiment configurations use components that would be removed: " + strings.Join(used, ", ") + "."}
}

// platformOwnershipMarker reads the marker the installed platform components carry.
func (s *Server) platformOwnershipMarker(r *http.Request, resource domain.ManagedResource) (string, *managedOperationFailure) {
	unavailable := &managedOperationFailure{status: http.StatusConflict, code: "ownership_unavailable", message: "The ownership marker of the installed platform is unavailable."}
	run, err := s.store.GetPipelineRun(r.Context(), resource.PipelineRunID)
	if err != nil {
		return "", unavailable
	}
	for _, stage := range run.Stages {
		if stage.StageID != "platform" || stage.OperationID == "" {
			continue
		}
		operation, err := s.store.GetOperation(r.Context(), stage.OperationID)
		if err != nil || len(operation.Plan.Steps) == 0 {
			return "", unavailable
		}
		var input struct {
			Marker string `json:"marker"`
		}
		if json.Unmarshal(operation.Plan.Steps[0].Input, &input) != nil || !ownershipMarkerPattern.MatchString(input.Marker) {
			return "", unavailable
		}
		return input.Marker, nil
	}
	return "", unavailable
}
