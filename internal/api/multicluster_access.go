package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	mesh "kubephos.dev/kubephos/plugins/cilium-mesh/runtime"
)

func (s *Server) downloadMultiClusterKubeconfig(response http.ResponseWriter, request *http.Request, resource domain.ManagedResource) {
	saved, _ := multiClusterSpec(resource.Spec)
	configs := map[string]string{}
	for index, member := range saved.Members {
		artifact, err := s.store.GetPipelineRunArtifactFromStage(request.Context(), resource.PipelineRunID, fmt.Sprintf("c%d-kubernetes", index+1), "cluster-connection")
		if err != nil || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
			writeError(response, 409, "kubeconfig_unavailable", "A verified member connection is unavailable.")
			return
		}
		raw, err := s.readArtifactPayload(request.Context(), artifact)
		if err != nil {
			writeError(response, 500, "artifact_error", "Could not decrypt a member connection.")
			return
		}
		config := clusterConnectionKubeconfig(raw)
		if config == "" {
			writeError(response, 409, "kubeconfig_invalid", "A member connection has no kubeconfig.")
			return
		}
		configs[member.Name] = config
	}
	raw, err := mesh.MergedKubeconfig(configs)
	if err != nil {
		writeError(response, 409, "kubeconfig_invalid", "Could not combine member kubeconfigs.")
		return
	}
	response.Header().Set("Content-Type", "application/yaml")
	response.Header().Set("Content-Disposition", `attachment; filename="`+resource.Name+`-kubeconfig.yaml"`)
	response.Header().Set("Cache-Control", "no-store")
	response.Write(raw)
}
func (s *Server) multiClusterResourceAccess(response http.ResponseWriter, request *http.Request, resource domain.ManagedResource, artifacts []domain.Artifact) {
	saved, _ := multiClusterSpec(resource.Spec)
	run, err := s.store.GetPipelineRun(request.Context(), resource.PipelineRunID)
	if err != nil {
		writeError(response, 500, "database_error", "Could not read member access.")
		return
	}
	items := []resourceAccessEndpoint{}
	for index, member := range saved.Members {
		operations := map[string]bool{}
		prefix := fmt.Sprintf("c%d-", index+1)
		for _, stage := range run.Stages {
			if strings.HasPrefix(stage.StageID, prefix) {
				operations[stage.OperationID] = true
			}
		}
		documents := []json.RawMessage{}
		host := ""
		config := ""
		for _, artifact := range artifacts {
			if !operations[artifact.OperationID] {
				continue
			}
			raw, err := s.readArtifactPayload(request.Context(), artifact)
			if err != nil || !json.Valid(raw) {
				continue
			}
			documents = append(documents, raw)
			if host == "" {
				host = accessHost(raw)
			}
			if artifact.Type == "ClusterConnection" {
				config = clusterConnectionKubeconfig(raw)
			}
		}
		if resource.Status == "ready" && config != "" && !hasHeadlampCredential(documents) {
			if token, err := readHeadlampAccessToken(request.Context(), config); err == nil {
				documents = append(documents, headlampAccessDocuments(token)...)
			}
		}
		for _, endpoint := range resourceAccessFromDocuments(documents, host) {
			endpoint.Name = member.Name + " · " + endpoint.Name
			items = append(items, endpoint)
		}
	}
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}
