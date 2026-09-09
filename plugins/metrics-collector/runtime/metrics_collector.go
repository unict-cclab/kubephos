package metricscollector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

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
	ClusterConnectionRef       string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef   string `json:"applicationDeploymentRef"`
	ObservabilityCapabilityRef string `json:"observabilityCapabilityRef"`
	Window                     string `json:"window"`
	Resolution                 string `json:"resolution"`
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
		ApplicationRef string `json:"applicationRef"`
		ClusterServer  string `json:"clusterServer"`
		Namespace      string `json:"namespace"`
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

type serviceEndpoint struct {
	Name        string `json:"name"`
	ServiceName string `json:"serviceName"`
	Port        int    `json:"port"`
	Scheme      string `json:"scheme"`
}

type metricDefinition struct {
	ID    string
	Unit  string
	Query string
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
	ApplicationRef string         `json:"applicationRef"`
	ClusterServer  string         `json:"clusterServer"`
	Namespace      string         `json:"namespace"`
	Start          time.Time      `json:"start"`
	End            time.Time      `json:"end"`
	StepSeconds    int            `json:"stepSeconds"`
	Source         datasetSource  `json:"source"`
	Series         []metricSeries `json:"series"`
	Summary        datasetSummary `json:"summary"`
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
	Metric string            `json:"metric"`
	Unit   string            `json:"unit"`
	Labels map[string]string `json:"labels"`
	Points []metricPoint     `json:"points"`
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
		ID: pluginID, Name: "Kubernetes metrics collector", Version: "0.1.0",
		Description:     "Collects a normalized workload dataset through a compatible metrics capability.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","observabilityCapabilityRef","window","resolution"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityCapabilityRef":{"type":"string","title":"Metrics service","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"},"window":{"type":"string","title":"Collection window","enum":["5m","15m","30m","1h"],"default":"15m"},"resolution":{"type":"string","title":"Data resolution","enum":["5s","15s","30s","1m"],"default":"15s"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "ObservabilityCapability", Version: "v1alpha1"}},
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
	refs := []struct{ path, value string }{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"applicationDeploymentRef", spec.ApplicationDeploymentRef}, {"observabilityCapabilityRef", spec.ObservabilityCapabilityRef}}
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
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "collect-workload-metrics", Name: "Collect normalized workload metrics", Input: input,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "observability-capability", Type: "ObservabilityCapability", Version: "v1alpha1", ArtifactID: spec.ObservabilityCapabilityRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "time-series-dataset", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", Source: "/dataset"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	_, cluster, deployment, capability, endpoint, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(cluster, deployment, capability, endpoint); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
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

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, deployment, capability, endpoint, err := resolve(step)
	if err != nil {
		return nil, err
	}
	window := windows()[spec.Window]
	resolution := resolutions()[spec.Resolution]
	end := plugin.now().UTC().Truncate(time.Second)
	start := end.Add(-window)
	definitions := metricDefinitions(deployment.Spec.Namespace)
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
	dataset := timeSeriesDataset{
		APIVersion: artifactAPI, Kind: "TimeSeriesDataset", Metadata: datasetMetadata{Name: deployment.Spec.Namespace + "/workload-overview", Version: "v1alpha1"},
		Spec: datasetSpec{
			ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace,
			Start: start, End: end, StepSeconds: int(resolution.Seconds()),
			Source: datasetSource{Kind: capability.Spec.MetricsAPI, Version: capability.Metadata.Version, Profile: "kubernetes-workload-overview/v1"},
			Series: allSeries, Summary: datasetSummary{Metrics: len(definitions), Series: len(allSeries), Samples: samples},
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
	var config struct {
		APIVersion     string `yaml:"apiVersion"`
		Kind           string `yaml:"kind"`
		CurrentContext string `yaml:"current-context"`
		Clusters       []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				Certificate string `yaml:"client-certificate-data"`
				Key         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(value), &config); err != nil || config.APIVersion != "v1" || config.Kind != "Config" || config.CurrentContext == "" {
		return errors.New("cluster kubeconfig is invalid")
	}
	var clusterName, userName string
	for _, item := range config.Contexts {
		if item.Name == config.CurrentContext {
			clusterName, userName = item.Context.Cluster, item.Context.User
		}
	}
	clusterOK := false
	for _, item := range config.Clusters {
		clusterOK = clusterOK || item.Name == clusterName && item.Cluster.Server == server
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name == userName {
			certificate, certificateErr := base64.StdEncoding.DecodeString(item.User.Certificate)
			key, keyErr := base64.StdEncoding.DecodeString(item.User.Key)
			userOK = certificateErr == nil && keyErr == nil && len(certificate) > 0 && len(key) > 0
		}
	}
	if !clusterOK || !userOK {
		return errors.New("cluster kubeconfig target or client identity is invalid")
	}
	return nil
}

func metricDefinitions(namespace string) []metricDefinition {
	selector := strconv.Quote(namespace)
	return []metricDefinition{
		{ID: "cpu.cores", Unit: "cores", Query: `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=` + selector + `,container!="",container!="POD"}[1m]))`},
		{ID: "memory.working_set", Unit: "bytes", Query: `sum by (pod) (container_memory_working_set_bytes{namespace=` + selector + `,container!="",container!="POD"})`},
		{ID: "pod.restarts", Unit: "count", Query: `sum by (pod) (kube_pod_container_status_restarts_total{namespace=` + selector + `})`},
		{ID: "deployment.ready_replicas", Unit: "replicas", Query: `max by (deployment) (kube_deployment_status_replicas_ready{namespace=` + selector + `})`},
	}
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
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return nil, errors.New("metrics API returned a non-finite value")
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
			series = append(series, metricSeries{Metric: definition.ID, Unit: definition.Unit, Labels: labels, Points: points})
		}
	}
	return series, nil
}

func validateDataset(dataset timeSeriesDataset, spec Spec, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability) error {
	window := windows()[spec.Window]
	resolution := resolutions()[spec.Resolution]
	if dataset.APIVersion != artifactAPI || dataset.Kind != "TimeSeriesDataset" || dataset.Metadata.Name != deployment.Spec.Namespace+"/workload-overview" || dataset.Metadata.Version != "v1alpha1" || dataset.Spec.ApplicationRef != deployment.Spec.ApplicationRef || dataset.Spec.ClusterServer != cluster.Spec.Server || dataset.Spec.Namespace != deployment.Spec.Namespace || dataset.Spec.Source.Kind != capability.Spec.MetricsAPI || dataset.Spec.Source.Version != capability.Metadata.Version || dataset.Spec.Source.Profile != "kubernetes-workload-overview/v1" {
		return errors.New("dataset identity, source or application binding is invalid")
	}
	if dataset.Spec.End.Before(dataset.Spec.Start) || dataset.Spec.End.Sub(dataset.Spec.Start) != window || dataset.Spec.StepSeconds != int(resolution.Seconds()) {
		return errors.New("dataset time window or resolution is invalid")
	}
	expected := map[string]bool{}
	for _, definition := range metricDefinitions(deployment.Spec.Namespace) {
		expected[definition.ID] = false
	}
	samples := 0
	for _, series := range dataset.Spec.Series {
		if _, ok := expected[series.Metric]; !ok || series.Unit == "" || len(series.Labels) == 0 || len(series.Points) == 0 {
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
	directory, err := os.MkdirTemp("", "kubephos-cluster-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	path := directory + "/kubeconfig"
	if err := os.WriteFile(path, []byte(kubeconfig), 0600); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
