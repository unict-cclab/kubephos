package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	applicationdeployer "kubephos.dev/kubephos/plugins/application-deployer/runtime"
)

type applicationDeploymentInput struct {
	Description       string                                 `json:"description,omitempty"`
	WorkspaceID       string                                 `json:"workspaceId"`
	ClusterResourceID string                                 `json:"clusterResourceId"`
	Name              string                                 `json:"name"`
	ApplicationRef    string                                 `json:"applicationRef"`
	Definition        experimentConfigurationDefinitionInput `json:"definition"`
	OwnerUserID       string                                 `json:"ownerUserId,omitempty"`
}

func (s *Server) listApplicationDeployments(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListManagedResources(r.Context(), "application-deployment")
	if err != nil {
		writeError(w, 500, "database_error", "Could not list application deployments.")
		return
	}
	result := []domain.ManagedResource{}
	for _, item := range items {
		var spec applicationDeploymentInput
		if json.Unmarshal(item.Spec, &spec) == nil && ownsResource(requestUser(r), spec.OwnerUserID) {
			result = append(result, item)
		}
	}
	writeJSON(w, 200, map[string]any{"items": result})
}

func (s *Server) createApplicationDeployment(w http.ResponseWriter, r *http.Request) {
	var input applicationDeploymentInput
	if decodeJSON(r, &input) != nil {
		writeError(w, 400, "invalid_request", "The deployment settings are invalid.")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Definition.Namespace = strings.TrimSpace(input.Definition.Namespace)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		writeError(w, 422, "invalid_name", "Name is required and must contain at most 120 characters.")
		return
	}
	if input.Definition.Namespace == "" {
		writeError(w, 422, "invalid_namespace", "Namespace is required.")
		return
	}
	if err := applicationdeployer.ValidateApplicationNamespace(input.Definition.Namespace); err != nil {
		writeError(w, 422, "invalid_namespace", err.Error())
		return
	}
	if input.Definition.DestinationMode == "external" || len(input.Definition.Components) > 0 || input.Definition.TwoPhaseStart {
		writeError(w, 422, "invalid_deployment", "Choose a catalog application without experiment components.")
		return
	}
	user := requestUser(r)
	if !s.validateClusterAssignment(w, r, user, input.ClusterResourceID) {
		return
	}
	cluster, err := s.store.GetManagedResource(r.Context(), input.ClusterResourceID)
	if err != nil || cluster.Kind != "kubernetes-cluster" || cluster.Status != "ready" {
		writeError(w, 409, "cluster_not_ready", "Select a ready Kubernetes cluster.")
		return
	}
	input.WorkspaceID = cluster.WorkspaceID
	input.OwnerUserID = user.ID
	normalized, failure := s.validatedExperimentConfiguration(r.Context(), experimentConfigurationInput{WorkspaceID: input.WorkspaceID, ClusterResourceID: input.ClusterResourceID, Name: input.Name, ApplicationRef: input.ApplicationRef, Definition: input.Definition})
	if failure != nil {
		failure.write(w)
		return
	}
	if json.Unmarshal(normalized.Definition, &input.Definition) != nil {
		writeError(w, 500, "invalid_configuration", "Could not prepare the application settings.")
		return
	}

	if err := s.store.CheckApplicationNamespaceAvailable(r.Context(), cluster.ID, input.Definition.Namespace); err != nil {
		if !errors.Is(err, storage.ErrConflict) {
			writeError(w, 500, "database_error", "Could not check namespace availability.")
			return
		}
		writeError(w, 409, "namespace_in_use", "This namespace is in use by an active experiment or application deployment on this cluster.")
		return
	}
	definition, failure := s.applicationDeploymentPipeline(r.Context(), input, cluster)
	if failure != nil {
		failure.write(w)
		return
	}
	validated, failure := s.validatedManagedPipeline(r.Context(), cluster.WorkspaceID, definition)
	if failure != nil {
		failure.write(w)
		return
	}
	resourceID := id.New("deployment")
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: cluster.WorkspaceID, Name: "Deploy " + input.Name + " · " + resourceID, Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := pipelineRunForManagedPipeline(pipeline, "Deploy "+input.Name)
	run.ClusterResourceID = cluster.ID
	run.OwnerUserID = user.ID
	run.IsolationMode = "namespaced"
	run.ApplicationNamespace = input.Definition.Namespace
	resource, err := s.store.CreateManagedPipelineResource(r.Context(), domain.ManagedResource{ID: resourceID, WorkspaceID: cluster.WorkspaceID, Name: input.Name, Kind: "application-deployment", Provider: "kubernetes", PluginID: "io.kubephos.applications.kubernetes.deploy", PluginVersion: validated.Resolution.Stages[len(validated.Resolution.Stages)-1].PluginVersion, PluginDigest: validated.Resolution.Stages[len(validated.Resolution.Stages)-1].PluginDigest, Spec: marshalRaw(input)}, pipeline, run, []domain.ManagedResourceDependency{{ResourceID: resourceID, DependsOnID: cluster.ID, Relation: "cluster"}})
	if err != nil {
		var pgerr *pgconn.PgError
		if errors.As(err, &pgerr) && pgerr.Code == "23505" {
			writeError(w, 409, "namespace_in_use", "This namespace is already reserved by an active experiment or application deployment.")
			return
		}
		writeError(w, 500, "database_error", "Could not queue the application deployment.")
		return
	}
	writeJSON(w, http.StatusAccepted, resource)
}

func (s *Server) deleteApplicationDeployment(w http.ResponseWriter, r *http.Request) {
	resource, err := s.store.GetManagedResource(r.Context(), r.PathValue("id"))
	var spec applicationDeploymentInput
	if err != nil || resource.Kind != "application-deployment" || json.Unmarshal(resource.Spec, &spec) != nil || !ownsResource(requestUser(r), spec.OwnerUserID) {
		writeError(w, 404, "not_found", "Application deployment not found.")
		return
	}
	// Cleanup is generated from the saved, ownership-checked lifecycle, including every member.
	s.deleteManagedPipelineResource(w, r, resource, "deployment")
}

func (s *Server) applicationDeploymentPipeline(ctx context.Context, input applicationDeploymentInput, cluster domain.ManagedResource) (domain.PipelineDefinition, *managedOperationFailure) {
	materializer, err := s.applicationMaterializer(ctx, input.ApplicationRef)
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: 422, code: "invalid_application", message: "Select an available catalog application."}
	}
	deployer, err := s.pluginForCapability("", "applications.kubernetes.deploy")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: 409, code: "capability_unavailable", message: "Application deployment is unavailable."}
	}
	values := input.Definition.ApplicationValues
	if len(values) == 0 {
		values = json.RawMessage(`{}`)
	}
	def := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "application", PluginID: materializer.Manifest().ID, Title: "Prepare application package", Spec: marshalRaw(map[string]any{"applicationRef": input.ApplicationRef, "values": values})}}}
	members := []string{cluster.Name}
	multi, group := multiClusterSpec(cluster.Spec)
	if group {
		members = nil
		for _, m := range multi.Members {
			members = append(members, m.Name)
		}
	}
	if len(members) == 0 {
		return def, &managedOperationFailure{status: 409, code: "cluster_invalid", message: "The cluster members are unavailable."}
	}
	for i, name := range members {
		var connection domain.Artifact
		if group {
			connection, err = s.store.GetPipelineRunArtifactFromStage(ctx, cluster.PipelineRunID, fmt.Sprintf("c%d-kubernetes", i+1), "cluster-connection")
		} else {
			connection, err = s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
		}
		if err != nil || connection.Type != "ClusterConnection" || connection.Version != "v1alpha1" {
			return def, &managedOperationFailure{status: 409, code: "cluster_invalid", message: "The connection for cluster " + name + " is unavailable."}
		}
		sources := map[string]experimentArtifactSource{artifactContractKey("ClusterConnection", "v1alpha1"): {ExternalID: connection.ID}}
		registerExperimentOutputs(sources, "application", materializer.Manifest())
		spec := map[string]any{"applicationNamespace": input.Definition.Namespace, "namespacePolicy": "create"}
		if input.Definition.ApplicationPlacement != nil {
			spec["applicationPlacement"] = input.Definition.ApplicationPlacement
		}
		if input.Definition.PodDisruptionBudgetMinAvailable > 0 {
			spec["podDisruptionBudgetMinAvailable"] = input.Definition.PodDisruptionBudgetMinAvailable
		}
		if input.Definition.IstioInjection != nil {
			spec["istioInjection"] = *input.Definition.IstioInjection
		}
		if len(input.Definition.IstioInjectionOverrides) > 0 {
			spec["istioInjectionOverrides"] = input.Definition.IstioInjectionOverrides
		}
		if group {
			spec["istioInjection"] = false
			spec["istioInjectionOverrides"] = map[string]bool{}
		}
		bindings, failure := experimentArtifactBindings(spec, deployer.Manifest(), sources, input.ApplicationRef)
		if failure != nil {
			return def, failure
		}
		stageID := fmt.Sprintf("deploy-%d", i+1)
		def.Stages = append(def.Stages, domain.PipelineStage{ID: stageID, PluginID: deployer.Manifest().ID, Title: "Deploy application · " + name, Spec: marshalRaw(spec), Bindings: bindings})
		def.Result = domain.PipelineOutput{Stage: stageID, Output: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1"}
	}
	return def, nil
}
