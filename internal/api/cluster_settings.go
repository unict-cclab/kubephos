package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/storage"
	k3s "kubephos.dev/kubephos/plugins/k3s/runtime"
	"net/http"
	"time"
)

type poolSettingsUpdate struct {
	Member string             `json:"member,omitempty"`
	Pool   string             `json:"pool"`
	Labels *map[string]string `json:"labels,omitempty"`
	Taints *[]placement.Taint `json:"taints,omitempty"`
}

// Change only pool metadata, retaining every other saved configuration field.
func changedPoolSettings(raw json.RawMessage, updates []poolSettingsUpdate) (json.RawMessage, []string, error) {
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		return nil, nil, errors.New("Saved cluster configuration is invalid.")
	}
	members := map[string]map[string]any{"": root}
	if root["mode"] == "multi" {
		members = map[string]map[string]any{}
		values, _ := root["members"].([]any)
		for _, item := range values {
			value, _ := item.(map[string]any)
			name, _ := value["name"].(string)
			members[name] = value
		}
	}
	changed := []string{}
	seen := map[string]bool{}
	for _, update := range updates {
		member, ok := members[update.Member]
		if !ok {
			return nil, nil, errors.New("Select a cluster belonging to this resource.")
		}
		identity := update.Member + "/" + update.Pool
		if seen[identity] {
			return nil, nil, errors.New("Each pool can be specified only once.")
		}
		seen[identity] = true
		pools, _ := member["applicationPools"].([]any)
		var selected map[string]any
		for _, item := range pools {
			pool, _ := item.(map[string]any)
			if pool["name"] == update.Pool {
				selected = pool
				break
			}
		}
		if selected == nil {
			return nil, nil, errors.New("Labels and taints can be edited only for existing application pools.")
		}
		if update.Labels == nil && update.Taints == nil {
			return nil, nil, errors.New("Specify labels or taints to update.")
		}
		if update.Labels != nil {
			if len(*update.Labels) == 0 {
				delete(selected, "labels")
			} else {
				selected["labels"] = *update.Labels
			}
		}
		if update.Taints != nil {
			if len(*update.Taints) == 0 {
				delete(selected, "taints")
			} else {
				selected["taints"] = *update.Taints
			}
		}
		data, _ := json.Marshal(selected)
		var pool managedNodePoolInput
		_ = json.Unmarshal(data, &pool)
		if err := placement.ValidatePool(placement.PoolSettings{Labels: pool.Labels, Taints: pool.Taints}); err != nil {
			return nil, nil, err
		}
		found := false
		for _, name := range changed {
			found = found || name == update.Member
		}
		if !found {
			changed = append(changed, update.Member)
		}
	}
	if len(updates) == 0 {
		return nil, nil, errors.New("Select at least one pool to update.")
	}
	data, err := json.Marshal(root)
	return data, changed, err
}

type clusterSettingsTarget struct {
	spec             k3s.Spec
	machines, access json.RawMessage
}

func (s *Server) updateClusterPoolSettings(w http.ResponseWriter, r *http.Request, resource domain.ManagedResource, input clusterMaintenanceInput) {
	if input.UpdatedAt == nil {
		writeError(w, 422, "invalid_settings", "The saved cluster version is required. Refresh the cluster and retry.")
		return
	}
	desired, members, err := changedPoolSettings(resource.Spec, *input.PoolSettings)
	if err != nil {
		writeError(w, 422, "invalid_settings", err.Error())
		return
	}
	var saved, next managedKubernetesClusterInput
	_ = json.Unmarshal(resource.Spec, &saved)
	_ = json.Unmarshal(desired, &next)
	run, err := s.store.GetPipelineRun(r.Context(), resource.PipelineRunID)
	if err != nil {
		writeError(w, 409, "workflow_unavailable", "Cluster lifecycle is unavailable.")
		return
	}
	targets := []clusterSettingsTarget{}
	for _, member := range members {
		oldMember, newMember := saved, next
		stageID := "kubernetes"
		if saved.Mode == "multi" {
			for i, value := range saved.Members {
				if value.Name == member {
					oldMember = value
					newMember = next.Members[i]
					stageID = fmt.Sprintf("c%d-kubernetes", i+1)
					break
				}
			}
		}
		operationID := ""
		for _, stage := range run.Stages {
			if stage.StageID == stageID {
				operationID = stage.OperationID
				break
			}
		}
		operation, err := s.store.GetOperation(r.Context(), operationID)
		if err != nil {
			writeError(w, 409, "workflow_unavailable", "Verified Kubernetes configuration is unavailable.")
			return
		}
		var spec k3s.Spec
		if json.Unmarshal(operation.Spec, &spec) != nil {
			writeError(w, 409, "workflow_unavailable", "Saved Kubernetes configuration is invalid.")
			return
		}
		spec.NodePools = managedClusterNodePools(newMember)
		spec.PreviousNodeSettings = managedApplicationNodeSettings(oldMember)
		target := clusterSettingsTarget{spec: spec}
		for index, id := range []string{spec.MachineSetRef, spec.MachineAccessRef} {
			artifact, err := s.store.GetArtifact(r.Context(), id)
			if err != nil || artifact.VerifiedAt.IsZero() || index == 0 && artifact.Type != "MachineSet" || index == 1 && (artifact.Type != "MachineAccess" || !artifact.Sensitive) {
				writeError(w, 409, "access_unavailable", "Verified node access is unavailable.")
				return
			}
			data, err := s.readArtifactPayload(r.Context(), artifact)
			if err != nil {
				writeError(w, 409, "access_unavailable", "Verified node access could not be read.")
				return
			}
			if index == 0 {
				target.machines = data
			} else {
				target.access = data
			}
		}
		targets = append(targets, target)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	var applyError error
	err = s.store.UpdateClusterSettings(ctx, resource.ID, *input.UpdatedAt, desired, func() error {
		for _, target := range targets {
			if err := k3s.ApplyPoolSettings(ctx, target.spec, target.machines, target.access); err != nil {
				applyError = fmt.Errorf("Could not update labels and taints on %s: %w", target.spec.ClusterName, err)
				return applyError
			}
		}
		return nil
	})
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "cluster_changed", "The cluster has changed or is being maintained. Refresh it and retry.")
		return
	}
	if err != nil {
		if applyError == nil {
			writeError(w, 500, "database_error", "Could not save cluster settings.")
			return
		}
		writeError(w, 502, "settings_update_failed", applyError.Error())
		return
	}
	updated, err := s.store.GetManagedResource(r.Context(), resource.ID)
	if err != nil {
		writeError(w, 500, "database_error", "Changes were saved, but the cluster could not be refreshed.")
		return
	}
	writeJSON(w, 200, updated)
}
