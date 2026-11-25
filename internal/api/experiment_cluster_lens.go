package api

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) experimentClusterLens(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.canReadResource(r, "experiments", id) {
		writeError(w, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	experiment, err := s.store.GetComparisonSource(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not read the experiment.")
		return
	}
	trial, found := clusterLensTrial(experiment, r.URL.Query().Get("trialId"))
	if experiment.Kind == "comparison" || !found {
		writeError(w, http.StatusConflict, "run_not_started", "ClusterLens is available once the run has started.")
		return
	}
	run, err := s.store.GetPipelineRun(r.Context(), trial.PipelineRunID)
	if err != nil || run.ClusterResourceID == "" || run.ApplicationNamespace == "" {
		writeError(w, http.StatusConflict, "namespace_unavailable", "This run has no application namespace on a cluster.")
		return
	}
	cluster, err := s.store.GetManagedResource(r.Context(), run.ClusterResourceID)
	if err != nil || cluster.Kind != "kubernetes-cluster" || cluster.Status != "ready" {
		writeError(w, http.StatusConflict, "cluster_unavailable", "The Kubernetes cluster is unavailable.")
		return
	}
	endpoint := clusterLensURL(cluster.Spec, run.ApplicationNamespace)
	if endpoint == "" {
		writeError(w, http.StatusConflict, "dashboard_unavailable", "ClusterLens is not installed on this cluster.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": endpoint})
}

// An explicit selection never falls back to another execution's namespace.
func clusterLensTrial(experiment domain.Experiment, id string) (domain.ExperimentTrial, bool) {
	if id == "" {
		return liveDashboardTrial(experiment)
	}
	for _, variant := range experiment.Variants {
		for _, trial := range variant.Trials {
			if trial.ID == id {
				return trial, trial.PipelineRunID != ""
			}
		}
	}
	return domain.ExperimentTrial{}, false
}

func clusterLensURL(raw json.RawMessage, namespace string) string {
	var spec struct {
		Mode         string            `json:"mode"`
		AddressStart string            `json:"addressStart"`
		Members      []json.RawMessage `json:"members"`
	}
	if json.Unmarshal(raw, &spec) != nil || namespace == "" {
		return ""
	}
	if spec.Mode == "multi" {
		for _, member := range spec.Members {
			if endpoint := clusterLensURL(member, namespace); endpoint != "" {
				return endpoint
			}
		}
		return ""
	}
	if net.ParseIP(spec.AddressStart) == nil || !clusterComponentSet(domain.ManagedResource{Spec: raw})["cluster-lens"] {
		return ""
	}
	for _, component := range managedcomponents.ClusterCatalog() {
		if component.ID != managedcomponents.ClusterLens {
			continue
		}
		for _, dashboard := range component.Dashboards {
			endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(spec.AddressStart, strconv.Itoa(dashboard.Port)), Path: dashboard.Path, RawQuery: url.Values{"namespace": {namespace}}.Encode()}
			return endpoint.String()
		}
	}
	return ""
}
