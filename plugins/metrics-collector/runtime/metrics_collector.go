package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"math"
	"net/url"

	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID    = "io.kubephos.metrics.prometheus.collect"
	artifactAPI = "artifacts.kubephos.dev/v1alpha1"
)

type Plugin struct {
	Runner commandRunner
	Clock  func() time.Time
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	Peers                      []ClusterPeer `json:"peers,omitempty"`
	ClusterName                string        `json:"clusterName,omitempty"`
	PeerOnly                   bool          `json:"peerOnly,omitempty"`
	ClusterConnectionRef       string        `json:"clusterConnectionRef"`
	ApplicationDeploymentRef   string        `json:"applicationDeploymentRef"`
	ObservabilityCapabilityRef string        `json:"observabilityCapabilityRef"`
	LoadSessionRef             string        `json:"loadSessionRef"`
	Window                     string        `json:"window"`
	Resolution                 string        `json:"resolution"`
	SLOMilliseconds            int           `json:"sloMilliseconds,omitempty"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Distribution string `json:"distribution"`
		Server       string `json:"server"`
		Kubeconfig   string `json:"kubeconfig"`
	} `json:"spec"`
}

type applicationDeployment struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name            string `json:"name"`
		Version         string `json:"version"`
		OwnershipMarker string `json:"ownershipMarker"`
	} `json:"metadata"`
	Spec struct {
		ControlMetrics *controlMetricsScope `json:"controlMetrics,omitempty"`
		ClusterName    string               `json:"clusterName,omitempty"`
		Networking     string               `json:"networking,omitempty"`
		ApplicationRef string               `json:"applicationRef"`
		External       bool                 `json:"external,omitempty"`
		ClusterServer  string               `json:"clusterServer"`
		Namespace      string               `json:"namespace"`
		Endpoints      []struct {
			Protocol string `json:"protocol"`
		} `json:"endpoints"`
		Workloads []struct {
			Name   string   `json:"name"`
			Traits []string `json:"traits"`
		} `json:"workloads"`
	} `json:"spec"`
}

type observabilityCapability struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		ClusterServer  string            `json:"clusterServer"`
		Namespace      string            `json:"namespace"`
		ScrapeInterval string            `json:"scrapeInterval"`
		Retention      string            `json:"retention"`
		Endpoints      []serviceEndpoint `json:"endpoints"`
		MetricsAPI     string            `json:"metricsAPI"`
	} `json:"spec"`
}

type loadSession struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		ApplicationRef  string     `json:"applicationRef"`
		ClusterServer   string     `json:"clusterServer"`
		Namespace       string     `json:"namespace"`
		ScenarioID      string     `json:"scenarioId"`
		State           string     `json:"state"`
		DurationSeconds int        `json:"durationSeconds"`
		StartedAt       *time.Time `json:"startedAt"`
		CompletedAt     *time.Time `json:"completedAt"`
		Steps           []loadStep `json:"steps"`
	} `json:"spec"`
}

type loadStep struct {
	Type            string  `json:"type"`
	DurationSeconds int     `json:"durationSeconds"`
	RPS             float64 `json:"rps"`
	BaselineRPS     float64 `json:"baselineRps"`
	AmplitudeRPS    float64 `json:"amplitudeRps"`
	PeriodSeconds   int     `json:"periodSeconds"`
	PhaseSeconds    int     `json:"phaseSeconds"`
	StartRPS        float64 `json:"startRps"`
	EndRPS          float64 `json:"endRps"`
	Curve           float64 `json:"curve"`
}

type serviceEndpoint struct {
	Name        string `json:"name"`
	ServiceName string `json:"serviceName"`
	Port        int    `json:"port"`
	Scheme      string `json:"scheme"`
}

type metricDefinition struct {
	ID          string
	Unit        string
	Aggregation string
	Query       string
}

type timeSeriesDataset struct {
	APIVersion string          `json:"apiVersion"`
	Kind       string          `json:"kind"`
	Metadata   datasetMetadata `json:"metadata"`
	Spec       datasetSpec     `json:"spec"`
}

type datasetMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type datasetSpec struct {
	Clusters          []string               `json:"clusters,omitempty"`
	EndToEndSource    string                 `json:"endToEndSource,omitempty"`
	WorkloadStartedAt *time.Time             `json:"workloadStartedAt,omitempty"`
	ControlMetrics    []controlMetricSummary `json:"controlMetrics,omitempty"`
	ApplicationRef    string                 `json:"applicationRef"`
	ClusterServer     string                 `json:"clusterServer"`
	Namespace         string                 `json:"namespace"`
	Start             time.Time              `json:"start"`
	End               time.Time              `json:"end"`
	StepSeconds       int                    `json:"stepSeconds"`
	SLOMilliseconds   int                    `json:"sloMilliseconds,omitempty"`
	Source            datasetSource          `json:"source"`
	Series            []metricSeries         `json:"series"`
	Summary           datasetSummary         `json:"summary"`
}

type datasetSource struct {
	Kind    string `json:"kind"`
	Version string `json:"version"`
	Profile string `json:"profile"`
}

type datasetSummary struct {
	Metrics int `json:"metrics"`
	Series  int `json:"series"`
	Samples int `json:"samples"`
}

type metricSeries struct {
	Metric      string            `json:"metric"`
	Unit        string            `json:"unit"`
	Aggregation string            `json:"aggregation"`
	Labels      map[string]string `json:"labels"`
	Points      []metricPoint     `json:"points"`
}

type metricPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

type result struct {
	Dataset timeSeriesDataset `json:"dataset"`
}

type prometheusResponse struct {
	Status    string `json:"status"`
	ErrorType string `json:"errorType"`
	Error     string `json:"error"`
	Data      struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string   `json:"metric"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

type commandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Kubernetes metrics collector", Version: "0.7.0",
		Description:     "Collects a normalized workload dataset through a compatible metrics capability.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","observabilityCapabilityRef","loadSessionRef","window","resolution"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityCapabilityRef":{"type":"string","title":"Metrics service","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"},"loadSessionRef":{"type":"string","title":"Completed load session","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadSession","x-kubephos-artifact-version":"v1alpha1"},"window":{"type":"string","title":"Metrics fallback window","x-kubephos-hidden":true,"enum":["5m","15m","30m","1h"],"default":"15m"},"resolution":{"type":"string","title":"Data resolution","enum":["5s","15s","30s","1m"],"default":"15s"},"sloMilliseconds":{"type":"integer","title":"Response time SLO (optional, ms)","minimum":1,"maximum":600000},"clusterName":{"type":"string","x-kubephos-hidden":true},"peers":{"type":"array","minItems":1,"maxItems":5,"x-kubephos-hidden":true,"items":{"type":"object","required":["name","clusterConnectionRef","applicationDeploymentRef","observabilityCapabilityRef"],"additionalProperties":false,"properties":{"name":{"type":"string"},"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityCapabilityRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"}}}}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "ObservabilityCapability", Version: "v1alpha1"}, {Type: "LoadSession", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "TimeSeriesDataset", Version: "v1alpha1"}},
		Capabilities:    []string{"metrics.timeseries.collect", "metrics.timeseries.preflight"},
		Permissions:     []string{"cluster.read", "network.http"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	refs := []struct{ path, value string }{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"applicationDeploymentRef", spec.ApplicationDeploymentRef}, {"observabilityCapabilityRef", spec.ObservabilityCapabilityRef}, {"loadSessionRef", spec.LoadSessionRef}}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.value, "art_") {
			return invalid(report, ref.path, "Select a verified artifact.")
		}
		if seen[ref.value] {
			return invalid(report, ref.path, "Each input must reference its own typed artifact.")
		}
		seen[ref.value] = true
	}
	if _, ok := windows()[spec.Window]; !ok {
		return invalid(report, "window", "Select a supported collection window.")
	}
	if _, ok := resolutions()[spec.Resolution]; !ok {
		return invalid(report, "resolution", "Select a supported data resolution.")
	}
	if spec.SLOMilliseconds < 0 || spec.SLOMilliseconds > 600000 {
		return invalid(report, "sloMilliseconds", "When set, the response time SLO must be between 1 and 600000 milliseconds.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will revalidate the cluster, application scope and metrics API before collecting the dataset."})
	return report
}

func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	if _, ok := windows()[spec.Window]; !ok {
		return domain.Plan{}, errors.New("unsupported collection window")
	}
	if _, ok := resolutions()[spec.Resolution]; !ok {
		return domain.Plan{}, errors.New("unsupported data resolution")
	}
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	plan := domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "collect-workload-metrics", Name: "Collect normalized workload metrics", Input: input,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "observability-capability", Type: "ObservabilityCapability", Version: "v1alpha1", ArtifactID: spec.ObservabilityCapabilityRef},
			{Name: "load-session", Type: "LoadSession", Version: "v1alpha1", ArtifactID: spec.LoadSessionRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "time-series-dataset", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", Source: "/dataset"}},
	}}}
	plan.Steps[0].ArtifactInputs = append(plan.Steps[0].ArtifactInputs, peerInputs(spec.Peers)...)
	return plan, nil
}

func (plugin Plugin) precheckSingle(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, deployment, capability, endpoint, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(cluster, deployment, capability, endpoint); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	load, err := resolveLoadSession(step)
	if err != nil || !spec.PeerOnly && validateLoadSession(load, cluster, deployment) != nil {
		return unhealthy("Completed load session is unavailable or invalid", "loadSession", "invalid"), nil
	}
	if err := log("info", "Validating cluster readiness, application scope, proxy authorization and metrics API"); err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Run(ctx, cluster.Spec.Kubeconfig, "auth", "can-i", "get", "services/proxy", "--namespace", capability.Spec.Namespace)
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("Kubernetes service proxy authorization check failed", "authorization", "denied"), nil
	}
	marker, err := runner.Run(ctx, cluster.Spec.Kubeconfig, "get", "namespace", deployment.Spec.Namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
	if err != nil || strings.TrimSpace(marker) != deployment.Metadata.OwnershipMarker {
		return unhealthy("Application namespace ownership does not match its deployment artifact", "application", "invalid"), nil
	}
	readyPath := proxyBase(capability, endpoint) + "/-/ready"
	ready, err := runner.Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+readyPath)
	if err != nil || strings.TrimSpace(ready) != "Prometheus Server is Ready." {
		return unhealthy("Metrics service readiness check failed", "metricsAPI", "unhealthy"), nil
	}
	queryPath := proxyBase(capability, endpoint) + "/api/v1/query?query=vector%281%29"
	response, err := runner.Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+queryPath)
	if err != nil || !successfulPrometheusResponse(response) {
		return unhealthy("Metrics API query validation failed", "query", "failed"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Metrics collection inputs are valid and ready", Checks: map[string]string{"api": "ready", "application": "owned", "authorization": "allowed", "metricsAPI": "ready"}}, nil
}

func (plugin Plugin) executeSingle(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, deployment, capability, endpoint, err := resolve(step)
	if err != nil {
		return nil, err
	}
	resolution := resolutions()[spec.Resolution]
	load, err := resolveLoadSession(step)
	if err != nil {
		return nil, err
	}
	start, end := metricWindow(load, plugin.now().UTC(), windows()[spec.Window])
	definitions := deploymentMetricDefinitions(deployment)
	if spec.PeerOnly {
		definitions = ciliumMetricDefinitions(deployment, true)
	}
	type queryResult struct {
		definition metricDefinition
		series     []metricSeries
		err        error
	}
	queryContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan queryResult, len(definitions))
	for _, definition := range definitions {
		definition := definition
		go func() {
			path := rangeQueryPath(capability, endpoint, definition.Query, start, end, resolution)
			response, queryErr := plugin.runner().Run(queryContext, cluster.Spec.Kubeconfig, "get", "--raw="+path)
			if queryErr != nil {
				results <- queryResult{definition: definition, err: queryErr}
				return
			}
			series, queryErr := decodeMatrix(response, definition, start, end)
			results <- queryResult{definition: definition, series: series, err: queryErr}
		}()
	}
	allSeries := []metricSeries{}
	if !spec.PeerOnly {
		allSeries = append(allSeries, configuredLoadSeries(load, start, end, resolution))
	}
	for range definitions {
		collected := <-results
		if collected.err != nil {
			cancel()
			return nil, fmt.Errorf("collect %s: %w", collected.definition.ID, collected.err)
		}
		if len(collected.series) == 0 {
			cancel()
			return nil, fmt.Errorf("collect %s: metrics service returned no series", collected.definition.ID)
		}
		if err := log("info", fmt.Sprintf("Collected %d %s series", len(collected.series), collected.definition.ID)); err != nil {
			cancel()
			return nil, err
		}
		allSeries = append(allSeries, collected.series...)
	}
	if spec.SLOMilliseconds > 0 && !spec.PeerOnly {
		for _, series := range allSeries {
			if series.Metric == "load.p95_response_time" {
				allSeries = append(allSeries, sloViolationSeries(series, spec.SLOMilliseconds))
				break
			}
		}
	}
	controlStart := start
	if scope := deployment.Spec.ControlMetrics; scope != nil && !scope.StartedAt.IsZero() && scope.StartedAt.Before(start) && end.Sub(scope.StartedAt) <= 24*time.Hour {
		controlStart = scope.StartedAt.UTC().Truncate(time.Second)
	}
	controlSeries, controlSummaries := plugin.collectControlMetrics(ctx, cluster, deployment, capability, endpoint, controlStart, end, resolution)
	allSeries = append(allSeries, controlSeries...)
	sort.Slice(allSeries, func(i, j int) bool {
		if allSeries[i].Metric != allSeries[j].Metric {
			return allSeries[i].Metric < allSeries[j].Metric
		}
		return labelIdentity(allSeries[i].Labels) < labelIdentity(allSeries[j].Labels)
	})
	samples := 0
	for _, series := range allSeries {
		samples += len(series.Points)
	}
	metricCount := len(definitions) + 1
	controlMetricIDs := map[string]bool{}
	for _, series := range controlSeries {
		controlMetricIDs[series.Metric] = true
	}
	metricCount += len(controlMetricIDs)
	if spec.SLOMilliseconds > 0 {
		metricCount++
	}
	dataset := timeSeriesDataset{
		APIVersion: artifactAPI, Kind: "TimeSeriesDataset", Metadata: datasetMetadata{Name: deployment.Spec.Namespace + "/workload-overview", Version: "v1alpha1"},
		Spec: datasetSpec{
			EndToEndSource: "locust", ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace,
			Start: controlStart, WorkloadStartedAt: &start, ControlMetrics: controlSummaries, End: end, StepSeconds: int(resolution.Seconds()), SLOMilliseconds: spec.SLOMilliseconds,
			Source: datasetSource{Kind: capability.Spec.MetricsAPI, Version: capability.Metadata.Version, Profile: "kubephos-experiment-evidence/v2"},
			Series: allSeries, Summary: datasetSummary{Metrics: metricCount, Series: len(allSeries), Samples: samples},
		},
	}
	return json.Marshal(result{Dataset: dataset})
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	spec, cluster, deployment, capability, _, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateDataset(value.Dataset, spec, cluster, deployment, capability); err != nil {
		return unhealthy(err.Error(), "dataset", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Verified %d metrics, %d series and %d samples", value.Dataset.Spec.Summary.Metrics, value.Dataset.Spec.Summary.Series, value.Dataset.Spec.Summary.Samples)); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Normalized metrics dataset is complete and verified", Checks: map[string]string{"metrics": strconv.Itoa(value.Dataset.Spec.Summary.Metrics), "series": strconv.Itoa(value.Dataset.Spec.Summary.Series), "samples": strconv.Itoa(value.Dataset.Spec.Summary.Samples), "source": capability.Spec.MetricsAPI}}, nil
}

func (Plugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, observabilityCapability, serviceEndpoint, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, err
	}
	clusterRaw, clusterOK := step.ResolvedInputs["cluster-connection"]
	deploymentRaw, deploymentOK := step.ResolvedInputs["application-deployment"]
	capabilityRaw, capabilityOK := step.ResolvedInputs["observability-capability"]
	if !clusterOK || !deploymentOK || !capabilityOK {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, errors.New("one or more verified metrics inputs are unavailable")
	}
	var cluster clusterConnection
	var deployment applicationDeployment
	var capability observabilityCapability
	if err := json.Unmarshal(clusterRaw.Value, &cluster); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, err
	}
	if err := json.Unmarshal(deploymentRaw.Value, &deployment); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, err
	}
	if err := json.Unmarshal(capabilityRaw.Value, &capability); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, err
	}
	var endpoint serviceEndpoint
	for _, candidate := range capability.Spec.Endpoints {
		if candidate.Name == "prometheus" {
			endpoint = candidate
		}
	}
	if endpoint.Name == "" {
		return Spec{}, clusterConnection{}, applicationDeployment{}, observabilityCapability{}, serviceEndpoint{}, errors.New("metrics capability has no Prometheus endpoint")
	}
	return spec, cluster, deployment, capability, endpoint, nil
}

func resolveLoadSession(step domain.PlanStep) (loadSession, error) {
	raw, ok := step.ResolvedInputs["load-session"]
	if !ok {
		return loadSession{}, errors.New("verified load session is unavailable")
	}
	var value loadSession
	if err := json.Unmarshal(raw.Value, &value); err != nil {
		return loadSession{}, err
	}
	return value, nil
}

func validateLoadSession(value loadSession, cluster clusterConnection, deployment applicationDeployment) error {
	if value.APIVersion != artifactAPI || value.Kind != "LoadSession" || value.Metadata.Name == "" || value.Metadata.Version != "v1alpha1" || value.Spec.ApplicationRef != deployment.Spec.ApplicationRef || value.Spec.ClusterServer != cluster.Spec.Server || value.Spec.Namespace != deployment.Spec.Namespace || value.Spec.ScenarioID == "" || value.Spec.State != "completed" || value.Spec.DurationSeconds < 1 || value.Spec.StartedAt == nil || value.Spec.CompletedAt == nil || value.Spec.CompletedAt.Before(*value.Spec.StartedAt) || value.Spec.CompletedAt.Sub(*value.Spec.StartedAt) < time.Duration(value.Spec.DurationSeconds)*time.Second || len(value.Spec.Steps) == 0 {
		return errors.New("load session identity, state or measurement interval is invalid")
	}
	return nil
}

func metricWindow(value loadSession, fallbackEnd time.Time, fallback time.Duration) (time.Time, time.Time) {
	if value.Spec.StartedAt != nil && value.Spec.CompletedAt != nil && value.Spec.CompletedAt.After(*value.Spec.StartedAt) {
		return value.Spec.StartedAt.UTC().Truncate(time.Second), value.Spec.CompletedAt.UTC().Truncate(time.Second)
	}
	end := fallbackEnd.UTC().Truncate(time.Second)
	return end.Add(-fallback), end
}

func configuredLoadSeries(value loadSession, start, end time.Time, resolution time.Duration) metricSeries {
	points := []metricPoint{}
	seriesResolution := configuredSeriesResolution(value, start, end, resolution)
	for current := start; !current.After(end); current = current.Add(seriesResolution) {
		points = append(points, metricPoint{Timestamp: current, Value: configuredRPS(value, current.Sub(start).Seconds())})
	}
	if len(points) == 0 || points[len(points)-1].Timestamp.Before(end) {
		points = append(points, metricPoint{Timestamp: end, Value: configuredRPS(value, end.Sub(start).Seconds())})
	}
	return metricSeries{Metric: "load.configured_rps", Unit: "requests_per_second", Aggregation: "sum", Labels: map[string]string{"scenario": value.Spec.ScenarioID}, Points: points}
}

func configuredSeriesResolution(value loadSession, start, end time.Time, resolution time.Duration) time.Duration {
	variable := false
	for _, step := range value.Spec.Steps {
		variable = variable || step.Type != "constant"
	}
	if !variable || resolution <= time.Second {
		return resolution
	}
	interval := time.Second
	const maximumPoints = 20000
	if duration := end.Sub(start); duration/interval > maximumPoints {
		interval = (duration + maximumPoints - 1) / maximumPoints
	}
	return interval
}

func configuredRPS(value loadSession, elapsed float64) float64 {
	cursor := 0.0
	for _, step := range value.Spec.Steps {
		duration := float64(step.DurationSeconds)
		if elapsed < cursor+duration {
			local := math.Max(0, elapsed-cursor)
			switch step.Type {
			case "constant":
				return math.Max(0, step.RPS)
			case "sinusoidal":
				if step.PeriodSeconds <= 0 {
					return 0
				}
				return math.Max(0, step.BaselineRPS+step.AmplitudeRPS*math.Sin(2*math.Pi*(local+float64(step.PhaseSeconds))/float64(step.PeriodSeconds)))
			case "exponential":
				progress := math.Min(1, local/math.Max(duration, 1))
				shaped := progress
				if math.Abs(step.Curve) >= 1e-9 {
					shaped = (math.Exp(step.Curve*progress) - 1) / (math.Exp(step.Curve) - 1)
				}
				return math.Max(0, step.StartRPS+(step.EndRPS-step.StartRPS)*shaped)
			}
		}
		cursor += duration
	}
	return 0
}

func validateArtifacts(cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability, endpoint serviceEndpoint) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Distribution == "" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if err := validateKubeconfig(cluster.Spec.Kubeconfig, cluster.Spec.Server); err != nil {
		return err
	}
	if deployment.APIVersion != artifactAPI || deployment.Kind != "ApplicationDeployment" || deployment.Metadata.Name == "" || deployment.Metadata.Version != "v1alpha1" || deployment.Metadata.OwnershipMarker == "" || deployment.Spec.ApplicationRef == "" || deployment.Spec.ClusterServer != cluster.Spec.Server || deployment.Spec.Namespace != deployment.Metadata.Name {
		return errors.New("application deployment identity is invalid")
	}

	if capability.APIVersion != artifactAPI || capability.Kind != "ObservabilityCapability" || capability.Metadata.Name == "" || capability.Metadata.Version == "" || capability.Spec.ClusterServer != cluster.Spec.Server || capability.Spec.Namespace == "" || capability.Spec.ScrapeInterval == "" || capability.Spec.Retention == "" || capability.Spec.MetricsAPI != "prometheus-v1" {
		return errors.New("observability capability identity or cluster binding is invalid")
	}
	if endpoint.ServiceName == "" || endpoint.Port < 1 || endpoint.Port > 65535 || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return errors.New("metrics service endpoint is invalid")
	}
	return nil
}

func validateKubeconfig(value, server string) error {
	return kubeconfigutil.ValidateClientIdentity(value, server)
}

func trafficEntryPointMatcher(deployment applicationDeployment) string {
	names := make([]string, 0)
	for _, workload := range deployment.Spec.Workloads {
		for _, trait := range workload.Traits {
			if trait == "traffic-entrypoint" {
				names = append(names, regexp.QuoteMeta(workload.Name))
				break
			}
		}
	}
	sort.Strings(names)
	return "^(?:" + strings.Join(names, "|") + ")$"
}

func metricDefinitions(namespace, _ string) []metricDefinition {
	selector := strconv.Quote(namespace)
	return append(locustMetricDefinitions(namespace), []metricDefinition{
		{ID: "application.request_rps", Unit: "requests_per_second", Aggregation: "sum", Query: `sum by (destination_workload) (rate(istio_requests_total{reporter="destination",destination_workload_namespace=` + selector + `}[1m]))`},
		{ID: "application.p95_response_time", Unit: "milliseconds", Aggregation: "mean", Query: `histogram_quantile(0.95, sum by (destination_workload, le) (rate(istio_request_duration_milliseconds_bucket{reporter="destination",destination_workload_namespace=` + selector + `}[1m])))`},
		{ID: "cpu.cores", Unit: "cores", Aggregation: "sum", Query: `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=` + selector + `,container!="",container!="POD",pod!~"kubephos-pod-scheduling-.*"}[1m]))`},
		{ID: "memory.working_set", Unit: "bytes", Aggregation: "sum", Query: `sum by (pod) (container_memory_working_set_bytes{namespace=` + selector + `,container!="",container!="POD",pod!~"kubephos-pod-scheduling-.*"})`},
		{ID: "pod.restarts", Unit: "count", Aggregation: "sum", Query: `sum by (pod) (kube_pod_container_status_restarts_total{namespace=` + selector + `,pod!~"kubephos-pod-scheduling-.*"})`},
		{ID: "deployment.ready_replicas", Unit: "replicas", Aggregation: "sum", Query: `max by (deployment) (kube_deployment_status_replicas_ready{namespace=` + selector + `,deployment!="kubephos-pod-scheduling"})`},
		{ID: "network.rtt", Unit: "milliseconds", Aggregation: "mean", Query: `1000 * sum by (origin_node, destination_node) (rate(node_latency_sum[1m])) / sum by (origin_node, destination_node) (rate(node_latency_count[1m]))`},
		{ID: "network.packet_loss", Unit: "percent", Aggregation: "mean", Query: `100 * avg_over_time(node_packet_loss_ratio[1m])`},
		{ID: "network.bandwidth", Unit: "bytes_per_second", Aggregation: "mean", Query: `node_bandwidth_bytes_per_second`},
	}...)
}

func nativeTCPDeployment(deployment applicationDeployment) bool {
	if len(deployment.Spec.Endpoints) == 0 {
		return false
	}
	for _, endpoint := range deployment.Spec.Endpoints {
		if endpoint.Protocol != "tcp" {
			return false
		}
	}
	return true
}

func deploymentMetricDefinitions(deployment applicationDeployment) []metricDefinition {
	if deployment.Spec.Networking == "cilium" {
		return ciliumMetricDefinitions(deployment, false)
	}
	if nativeTCPDeployment(deployment) {
		return nativeMetricDefinitions(deployment.Spec.Namespace)
	}

	definitions := metricDefinitions(deployment.Spec.Namespace, trafficEntryPointMatcher(deployment))
	if !deployment.Spec.External {
		return definitions
	}
	filtered := make([]metricDefinition, 0, len(definitions)-2)
	for _, definition := range definitions {
		if strings.HasPrefix(definition.ID, "application.") {
			continue
		}
		filtered = append(filtered, definition)
	}
	return filtered
}

// Locust measures all end-to-end requests at the load generator.
func locustMetricDefinitions(namespace string) []metricDefinition {
	selector := `namespace=` + strconv.Quote(namespace)
	requests := `kubephos_load_requests_total{` + selector
	success := `sum(rate(` + requests + `,outcome="success"}[1m]))`
	failed := `sum(rate(` + requests + `,outcome="failure"}[1m]))`
	total := `sum(rate(` + requests + `}[1m]))`
	return []metricDefinition{
		{ID: "load.successful_rps", Unit: "requests_per_second", Aggregation: "sum", Query: success},
		{ID: "load.failed_rps", Unit: "requests_per_second", Aggregation: "sum", Query: failed},
		{ID: "load.failure_percentage", Unit: "percent", Aggregation: "mean", Query: `100 * ` + failed + ` / clamp_min(` + total + `, 0.000001)`},
		{ID: "load.p95_response_time", Unit: "milliseconds", Aggregation: "mean", Query: `histogram_quantile(0.95, sum by (le) (rate(kubephos_load_duration_milliseconds_bucket{` + selector + `}[1m])))`},
	}
}

// Native protocol latency includes Redis redirects, without an HTTP proxy.
func nativeMetricDefinitions(namespace string) []metricDefinition {
	selector := `namespace=` + strconv.Quote(namespace)
	requests := `kubephos_load_requests_total{` + selector
	definitions := append(locustMetricDefinitions(namespace), []metricDefinition{
		{ID: "redis.operation_rps", Unit: "requests_per_second", Aggregation: "sum", Query: `sum by (operation) (rate(` + requests + `}[1m]))`},
		{ID: "redis.operation_p95_response_time", Unit: "milliseconds", Aggregation: "mean", Query: `histogram_quantile(0.95, sum by (operation, le) (rate(kubephos_load_duration_milliseconds_bucket{` + selector + `}[1m])))`},
	}...)
	for _, definition := range metricDefinitions(namespace, "") {
		if strings.HasPrefix(definition.ID, "load.") || strings.HasPrefix(definition.ID, "application.") {
			continue
		}
		if definition.ID == "deployment.ready_replicas" {
			definition.Query = `max by (statefulset) (kube_statefulset_status_replicas_ready{` + selector + `})`
		}
		definitions = append(definitions, definition)
	}
	return definitions
}

func sloViolationSeries(responseTime metricSeries, sloMilliseconds int) metricSeries {
	points := make([]metricPoint, len(responseTime.Points))
	for index, point := range responseTime.Points {
		value := 0.0
		if point.Value > float64(sloMilliseconds) {
			value = 100
		}
		points[index] = metricPoint{Timestamp: point.Timestamp, Value: value}
	}
	return metricSeries{Metric: "load.slo_violation_percentage", Unit: "percent", Aggregation: "mean", Labels: map[string]string{"objective": strconv.Itoa(sloMilliseconds) + "ms"}, Points: points}
}

func proxyBase(capability observabilityCapability, endpoint serviceEndpoint) string {
	return "/api/v1/namespaces/" + url.PathEscape(capability.Spec.Namespace) + "/services/" + endpoint.Scheme + ":" + url.PathEscape(endpoint.ServiceName) + ":" + strconv.Itoa(endpoint.Port) + "/proxy"
}

func rangeQueryPath(capability observabilityCapability, endpoint serviceEndpoint, query string, start, end time.Time, resolution time.Duration) string {
	values := url.Values{}
	values.Set("query", query)
	values.Set("start", strconv.FormatInt(start.Unix(), 10))
	values.Set("end", strconv.FormatInt(end.Unix(), 10))
	values.Set("step", strconv.Itoa(int(resolution.Seconds())))
	return proxyBase(capability, endpoint) + "/api/v1/query_range?" + values.Encode()
}

func successfulPrometheusResponse(raw string) bool {
	var response struct {
		Status string `json:"status"`
	}
	return json.Unmarshal([]byte(raw), &response) == nil && response.Status == "success"
}

func decodeMatrix(raw string, definition metricDefinition, start, end time.Time) ([]metricSeries, error) {
	var response prometheusResponse
	if err := json.Unmarshal([]byte(raw), &response); err != nil {
		return nil, fmt.Errorf("decode metrics response: %w", err)
	}
	if response.Status != "success" {
		return nil, fmt.Errorf("metrics API %s: %s", response.ErrorType, response.Error)
	}
	if response.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("metrics API returned %s instead of matrix", response.Data.ResultType)
	}
	series := make([]metricSeries, 0, len(response.Data.Result))
	for _, item := range response.Data.Result {
		labels := map[string]string{}
		for key, value := range item.Metric {
			if key != "__name__" {
				labels[key] = value
			}
		}
		points := make([]metricPoint, 0, len(item.Values))
		for _, sample := range item.Values {
			if len(sample) != 2 {
				return nil, errors.New("metrics API returned a malformed sample")
			}
			var timestamp float64
			var encodedValue string
			if err := json.Unmarshal(sample[0], &timestamp); err != nil {
				return nil, errors.New("metrics API returned an invalid timestamp")
			}
			if err := json.Unmarshal(sample[1], &encodedValue); err != nil {
				return nil, errors.New("metrics API returned an invalid value")
			}
			value, err := strconv.ParseFloat(encodedValue, 64)
			if err != nil {
				return nil, errors.New("metrics API returned a non-finite value")
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				continue
			}
			seconds, fraction := math.Modf(timestamp)
			pointTime := time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
			if pointTime.Before(start) || pointTime.After(end) {
				return nil, errors.New("metrics API returned a sample outside the requested window")
			}
			if len(points) > 0 && !pointTime.After(points[len(points)-1].Timestamp) {
				return nil, errors.New("metrics API returned unordered or duplicate samples")
			}
			points = append(points, metricPoint{Timestamp: pointTime, Value: value})
		}
		if len(points) > 0 {
			series = append(series, metricSeries{Metric: definition.ID, Unit: definition.Unit, Aggregation: definition.Aggregation, Labels: labels, Points: points})
		}
	}
	return series, nil
}

func validateDataset(dataset timeSeriesDataset, spec Spec, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability) error {
	resolution := resolutions()[spec.Resolution]
	if dataset.APIVersion != artifactAPI || dataset.Kind != "TimeSeriesDataset" || dataset.Metadata.Name != deployment.Spec.Namespace+"/workload-overview" || dataset.Metadata.Version != "v1alpha1" || dataset.Spec.ApplicationRef != deployment.Spec.ApplicationRef || dataset.Spec.ClusterServer != cluster.Spec.Server || dataset.Spec.Namespace != deployment.Spec.Namespace || dataset.Spec.Source.Kind != capability.Spec.MetricsAPI || dataset.Spec.Source.Version != capability.Metadata.Version || dataset.Spec.Source.Profile != "kubephos-experiment-evidence/v2" {
		return errors.New("dataset identity, source or application binding is invalid")
	}
	if !dataset.Spec.End.After(dataset.Spec.Start) || dataset.Spec.End.Sub(dataset.Spec.Start) > 24*time.Hour+10*time.Minute || dataset.Spec.StepSeconds != int(resolution.Seconds()) {
		return errors.New("dataset time window or resolution is invalid")
	}
	if dataset.Spec.SLOMilliseconds != spec.SLOMilliseconds || dataset.Spec.SLOMilliseconds < 0 {
		return errors.New("dataset response time objective is invalid")
	}
	expected := map[string]bool{"load.configured_rps": false}
	if spec.SLOMilliseconds > 0 {
		expected["load.slo_violation_percentage"] = false
	}
	for _, definition := range deploymentMetricDefinitions(deployment) {
		expected[definition.ID] = false
	}
	controlIDs := map[string]bool{}
	for _, summary := range dataset.Spec.ControlMetrics {
		if deployment.Spec.ControlMetrics == nil || controlIDs[summary.Metric] || (summary.Metric == "scheduler.pod_scheduling_latency" && !deployment.Spec.ControlMetrics.PodScheduling) {
			return errors.New("unexpected control metric summary")
		}
		if err := validateControlSummary(summary); err != nil {
			return err
		}
		controlIDs[summary.Metric] = true
		if summary.Status == "available" {
			expected[summary.Metric] = false
		}
	}
	samples := 0
	for _, series := range dataset.Spec.Series {
		if _, ok := expected[series.Metric]; !ok || series.Unit == "" || (series.Aggregation != "sum" && series.Aggregation != "mean") || len(series.Points) == 0 {
			return errors.New("dataset contains an invalid metric series")
		}
		expected[series.Metric] = true
		for index, point := range series.Points {
			if math.IsNaN(point.Value) || math.IsInf(point.Value, 0) || point.Timestamp.Before(dataset.Spec.Start) || point.Timestamp.After(dataset.Spec.End) || index > 0 && !point.Timestamp.After(series.Points[index-1].Timestamp) {
				return errors.New("dataset contains an invalid sample")
			}
		}
		samples += len(series.Points)
	}
	for _, present := range expected {
		if !present {
			return errors.New("dataset is missing one or more managed metrics")
		}
	}
	if dataset.Spec.Summary.Metrics != len(expected) || dataset.Spec.Summary.Series != len(dataset.Spec.Series) || dataset.Spec.Summary.Samples != samples || samples == 0 {
		return errors.New("dataset summary does not match its contents")
	}
	return nil
}

func labelIdentity(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+labels[key])
	}
	return strings.Join(values, ",")
}

func windows() map[string]time.Duration {
	return map[string]time.Duration{"5m": 5 * time.Minute, "15m": 15 * time.Minute, "30m": 30 * time.Minute, "1h": time.Hour}
}

func resolutions() map[string]time.Duration {
	return map[string]time.Duration{"5s": 5 * time.Second, "15s": 15 * time.Second, "30s": 30 * time.Second, "1m": time.Minute}
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return localRunner{}
}

func (plugin Plugin) now() time.Time {
	if plugin.Clock != nil {
		return plugin.Clock()
	}
	return time.Now()
}

type localRunner struct{}

func (localRunner) Run(ctx context.Context, kubeconfig string, args ...string) (string, error) {
	output, err := kubeconfigutil.RunKubectl(ctx, kubeconfig, nil, args...)
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
