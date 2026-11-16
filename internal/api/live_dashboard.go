package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/grafanadashboards"
	"kubephos.dev/kubephos/internal/storage"
)

// liveDashboardClient reaches the managed Grafana exposed by each cluster.
var liveDashboardClient = &http.Client{Timeout: 10 * time.Second}

// experimentLiveDashboard opens the live Grafana dashboard of a run: it makes
// sure the dashboard is installed on the run's cluster, then returns its address
// scoped to the run namespace and start time. The current run of a suite is
// preferred; otherwise the most recently started one is shown.
func (s *Server) experimentLiveDashboard(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if !s.canReadResource(request, "experiments", id) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	experiment, err := s.store.GetComparisonSource(request.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment.")
		return
	}
	trial, found := liveDashboardTrial(experiment)
	if experiment.Kind == "comparison" || !found {
		writeError(response, http.StatusConflict, "run_not_started", "The live dashboard opens once a run has started on a cluster.")
		return
	}
	run, err := s.store.GetPipelineRun(request.Context(), trial.PipelineRunID)
	if err != nil || run.ClusterResourceID == "" {
		writeError(response, http.StatusConflict, "run_not_started", "The live dashboard opens once a run has started on a cluster.")
		return
	}
	base, failure := s.liveDashboardBase(request.Context(), run.ClusterResourceID)
	if failure != nil {
		writeError(response, failure.status, failure.code, failure.message)
		return
	}
	from := time.Time{}
	if trial.StartedAt != nil {
		from = trial.StartedAt.Add(-time.Minute)
	}
	writeJSON(response, http.StatusOK, map[string]string{"url": grafanadashboards.ExperimentURL(base, run.ApplicationNamespace, from)})
}

// clusterLiveDashboard opens the same dashboard from the cluster details; it
// selects the namespace currently receiving load.
func (s *Server) clusterLiveDashboard(response http.ResponseWriter, request *http.Request) {
	id := request.PathValue("id")
	if !s.validateClusterAssignment(response, request, requestUser(request), id) {
		return
	}
	base, failure := s.liveDashboardBase(request.Context(), id)
	if failure != nil {
		writeError(response, failure.status, failure.code, failure.message)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"url": grafanadashboards.ExperimentURL(base, "", time.Time{})})
}

func liveDashboardTrial(experiment domain.Experiment) (domain.ExperimentTrial, bool) {
	trials := []domain.ExperimentTrial{}
	for _, variant := range experiment.Variants {
		for _, trial := range variant.Trials {
			if trial.PipelineRunID != "" {
				trials = append(trials, trial)
			}
		}
	}
	for _, trial := range trials {
		if trial.Status == "running" || trial.Status == "initialized" || trial.Status == "initializing" {
			return trial, true
		}
	}
	started := func(trial domain.ExperimentTrial) time.Time {
		if trial.StartedAt != nil {
			return *trial.StartedAt
		}
		return time.Time{}
	}
	sort.SliceStable(trials, func(i, j int) bool { return started(trials[i]).After(started(trials[j])) })
	if len(trials) == 0 || trials[0].StartedAt == nil {
		return domain.ExperimentTrial{}, false
	}
	return trials[0], true
}

// liveDashboardBase resolves the Grafana address and administrator identity
// recorded by the cluster's managed observability, and installs or updates the
// live dashboard there.
func (s *Server) liveDashboardBase(ctx context.Context, clusterID string) (string, *managedOperationFailure) {
	cluster, err := s.store.GetManagedResource(ctx, clusterID)
	if err != nil || cluster.Kind != "kubernetes-cluster" {
		return "", &managedOperationFailure{status: http.StatusNotFound, code: "not_found", message: "Kubernetes cluster not found."}
	}
	if cluster.Status != "ready" {
		return "", &managedOperationFailure{status: http.StatusConflict, code: "cluster_not_ready", message: "The live dashboard is available once the cluster is ready."}
	}
	if _, multi := multiClusterSpec(cluster.Spec); multi {
		return "", &managedOperationFailure{status: http.StatusConflict, code: "dashboard_unavailable", message: "The live dashboard is available on single clusters."}
	}
	unavailable := &managedOperationFailure{status: http.StatusConflict, code: "dashboard_unavailable", message: "Managed Grafana details are unavailable for this cluster."}
	read := func(name, kind string) ([]byte, bool) {
		artifact, err := s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, name)
		if err != nil || artifact.VerifiedAt.IsZero() {
			return nil, false
		}
		raw, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			return nil, false
		}
		var document struct {
			Kind string `json:"kind"`
		}
		return raw, json.Unmarshal(raw, &document) == nil && document.Kind == kind
	}
	connection, ok := read("cluster-connection", "ClusterConnection")
	if !ok {
		return "", unavailable
	}
	capability, ok := read("observability-capability", "ObservabilityCapability")
	if !ok {
		return "", unavailable
	}
	credential, ok := read("grafana-credential", "ServiceCredential")
	if !ok {
		return "", unavailable
	}
	base, identity, err := grafanaAccess(accessHost(connection), capability, credential)
	if err != nil {
		return "", unavailable
	}
	ensureContext, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	grafana := grafanadashboards.Grafana{BaseURL: base, Username: identity.Spec.Username, Password: identity.Spec.Password, Client: liveDashboardClient}
	if err := grafana.EnsureExperiment(ensureContext); err != nil {
		return "", &managedOperationFailure{status: http.StatusBadGateway, code: "grafana_unavailable", message: "Could not prepare the live dashboard in Grafana: " + err.Error()}
	}
	return base, nil
}

type grafanaIdentity struct {
	Spec struct {
		Service  string `json:"service"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"spec"`
}

// grafanaAccess pairs the Grafana endpoint of the observability capability with
// its administrator credential, using the cluster address like the dashboards.
func grafanaAccess(host string, capabilityRaw, credentialRaw []byte) (string, grafanaIdentity, error) {
	var capability struct {
		Spec struct {
			Endpoints []struct {
				Name        string `json:"name"`
				ServiceName string `json:"serviceName"`
				Scheme      string `json:"scheme"`
				NodePort    int    `json:"nodePort"`
			} `json:"endpoints"`
		} `json:"spec"`
	}
	var identity grafanaIdentity
	if host == "" || json.Unmarshal(capabilityRaw, &capability) != nil || json.Unmarshal(credentialRaw, &identity) != nil {
		return "", identity, errors.New("managed Grafana details are incomplete")
	}
	for _, endpoint := range capability.Spec.Endpoints {
		if endpoint.Name != "grafana" || endpoint.NodePort <= 0 {
			continue
		}
		if identity.Spec.Username == "" || identity.Spec.Password == "" || identity.Spec.Service != "" && identity.Spec.Service != endpoint.ServiceName {
			return "", identity, errors.New("managed Grafana credential does not match its endpoint")
		}
		scheme := strings.ToLower(endpoint.Scheme)
		if scheme != "https" {
			scheme = "http"
		}
		return fmt.Sprintf("%s://%s:%d", scheme, host, endpoint.NodePort), identity, nil
	}
	return "", identity, errors.New("managed Grafana endpoint is unavailable")
}
