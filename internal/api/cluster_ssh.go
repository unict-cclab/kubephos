package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"

	"golang.org/x/crypto/ssh"
	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) downloadClusterSSHKey(response http.ResponseWriter, request *http.Request) {
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
		writeError(response, http.StatusConflict, "cluster_not_ready", "SSH access is available only after every cluster health gate passes.")
		return
	}
	stage, name := "machines", resource.Name
	memberName := request.URL.Query().Get("cluster")
	if saved, multi := multiClusterSpec(resource.Spec); multi {
		stage = ""
		if saved.SharedSSHKey && memberName == "" {
			stage = "c1-machines"
		}
		for index, member := range saved.Members {
			if member.Name == memberName {
				stage, name = fmt.Sprintf("c%d-machines", index+1), member.Name
				break
			}
		}
		if stage == "" {
			writeError(response, http.StatusBadRequest, "invalid_cluster", "Select a cluster belonging to this multi cluster.")
			return
		}
		if saved.SharedSSHKey {
			stage, name = "c1-machines", resource.Name
		}
	} else if memberName != "" {
		writeError(response, http.StatusBadRequest, "invalid_cluster", "This resource is a single cluster.")
		return
	}
	artifact, err := s.store.GetPipelineRunArtifactFromStage(request.Context(), resource.PipelineRunID, stage, "machine-access")
	if err != nil || artifact.Type != "MachineAccess" || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
		writeError(response, http.StatusConflict, "ssh_key_unavailable", "The verified machine SSH key is unavailable.")
		return
	}
	raw, err := s.readArtifactPayload(request.Context(), artifact)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "artifact_error", "Could not decrypt the machine SSH key.")
		return
	}
	var access struct {
		Kind string `json:"kind"`
		Spec struct {
			PrivateKey string `json:"privateKey"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &access) != nil || access.Kind != "MachineAccess" {
		writeError(response, http.StatusConflict, "ssh_key_invalid", "The stored machine SSH key is invalid.")
		return
	}
	key := strings.TrimSpace(access.Spec.PrivateKey) + "\n"
	if _, err := ssh.ParsePrivateKey([]byte(key)); err != nil {
		writeError(response, http.StatusConflict, "ssh_key_invalid", "The stored machine SSH key is invalid.")
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + "-ssh-key.pem"}))
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write([]byte(key))
}
