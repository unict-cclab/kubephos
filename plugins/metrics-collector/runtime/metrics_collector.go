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
	// RateWindow is the range used by rate(), histogram_quantile() and
	// avg_over_time(): "auto" (default) or an explicit PromQL duration.
	RateWindow string `json:"rateWindow,omitempty"`
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
		NamespaceCreatedAt *time.Time           `json:"namespaceCreatedAt,omitempty"`
		ControlMetrics     *controlMetricsScope `json:"controlMetrics,omitempty"`
		ClusterName        string               `json:"clusterName,omitempty"`
		Networking         string               `json:"networking,omitempty"`
		ApplicationRef     string               `json:"applicationRef"`
		External           bool                 `json:"external,omitempty"`
		ClusterServer      string               `json:"clusterServer"`
		Namespace          string               `json:"namespace"`
		Endpoints          []struct {
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
		Replicas        int        `json:"replicas,omitempty"`
		// Optional fields of newer load sessions; absent on legacy artifacts.
		WarmupSeconds   *int            `json:"warmupSeconds,omitempty"`
		WarmupStartedAt *time.Time      `json:"warmupStartedAt,omitempty"`
		DrainSeconds    *int            `json:"drainSeconds,omitempty"`
		Clock           string          `json:"clock,omitempty"`
		Workers         int             `json:"workers,omitempty"`
		LocustSummary   json.RawMessage `json:"locustSummary,omitempty"`
		// Request mix and call-chain profiling (optional). callChainProfile
		// stays raw so that a malformed value never fails the run.
		TaskMix          json.RawMessage `json:"taskMix,omitempty"`
		CallChainProfile json.RawMessage `json:"callChainProfile,omitempty"`
	} `json:"spec"`
}

type loadStep struct {
	Type            string  `json:"type"`
	DurationSeconds int     `json:"durationSeconds"`
	RPS             float64 `json:"rps"`
	BaselineRPS     float64 `json:"baselineRps"`
	AmplitudeRPS    float64 `json:"amplitudeRps"`
	// The configuration editor saves sinusoidal steps as a range instead of baseline and amplitude.
	MinRPS        *float64 `json:"minRps,omitempty"`
	MaxRPS        *float64 `json:"maxRps,omitempty"`
	PeriodSeconds int      `json:"periodSeconds"`
	PhaseSeconds  int      `json:"phaseSeconds"`
	StartRPS      float64  `json:"startRps"`
	EndRPS        float64  `json:"endRps"`
	Curve         float64  `json:"curve"`
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
	// Provenance lives next to the query so the published catalog cannot
	// drift from what was actually measured.
	Source      string
	Vantage     string
	Description string
	// Optional metrics may legitimately have no series (older load generator
	// image, idle windows, no mesh traffic); they never fail a run.
	Optional bool
	// RateWindowSeconds is set by applyRateWindow when the query uses a range.
	RateWindowSeconds int
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
	Placement         *placementHistory      `json:"placement,omitempty"`
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
	// Window of rate()/histogram_quantile()/avg_over_time() ranges, in seconds.
	RateWindowSeconds int `json:"rateWindowSeconds,omitempty"`
	// Scrape interval of the load generator's Prometheus job, when detected.
	ScrapeIntervalSeconds int                  `json:"scrapeIntervalSeconds,omitempty"`
	Metrics               []metricCatalogEntry `json:"metrics,omitempty"`
	LocustSummary         json.RawMessage      `json:"locustSummary,omitempty"`
	TaskMix               json.RawMessage      `json:"taskMix,omitempty"`
	CallModel             *callModel           `json:"callModel,omitempty"`
	RunContext            *runContext          `json:"runContext,omitempty"`
	Source                datasetSource        `json:"source"`
	Series                []metricSeries       `json:"series"`
	Summary               datasetSummary       `json:"summary"`
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
		ID: pluginID, Name: "Kubernetes metrics collector", Version: "0.9.0",
		Description:     "Collects a normalized workload dataset through a compatible metrics capability.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","observabilityCapabilityRef","loadSessionRef","window","resolution"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityCapabilityRef":{"type":"string","title":"Metrics service","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"},"loadSessionRef":{"type":"string","title":"Completed load session","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadSession","x-kubephos-artifact-version":"v1alpha1"},"window":{"type":"string","title":"Metrics fallback window","x-kubephos-hidden":true,"enum":["5m","15m","30m","1h"],"default":"15m"},"resolution":{"type":"string","title":"Data resolution","enum":["5s","15s","30s","1m"],"default":"15s"},"sloMilliseconds":{"type":"integer","title":"Response time SLO (optional, ms)","minimum":1,"maximum":600000},"rateWindow":{"type":"string","title":"Rate window","description":"Range used by rate() and percentile queries. Auto uses max(resolution, 4 x scrape interval).","enum":["auto","30s","1m","2m","5m"],"default":"auto"},"clusterName":{"type":"string","x-kubephos-hidden":true},"peers":{"type":"array","minItems":1,"maxItems":5,"x-kubephos-hidden":true,"items":{"type":"object","required":["name","clusterConnectionRef","applicationDeploymentRef","observabilityCapabilityRef"],"additionalProperties":false,"properties":{"name":{"type":"string"},"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"observabilityCapabilityRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"}}}}}}`),
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
	if !validRateWindow(spec.RateWindow) {
		return invalid(report, "rateWindow", "Select a supported rate window.")
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
	if !validRateWindow(spec.RateWindow) {
		return domain.Plan{}, errors.New("unsupported rate window")
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

// executeSingle collects one cluster. rateWindow forces the range of rate
// queries (peers reuse the primary cluster's window); zero selects it from
// the configuration and the detected scrape interval.
func (plugin Plugin) executeSingle(ctx context.Context, step domain.PlanStep, log plugins.Logger, rateWindow time.Duration) (json.RawMessage, error) {
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
	scrapeInterval, scrapeJob := plugin.detectScrapeInterval(ctx, cluster, capability, endpoint, deployment.Spec.Namespace)
	if rateWindow <= 0 {
		rateWindow = selectRateWindow(spec.RateWindow, resolution, scrapeInterval)
	}
	if scrapeInterval > 0 {
		if err := log("info", fmt.Sprintf("Using a %s rate window (scrape interval %s of %s, resolution %s)", formatPromDuration(rateWindow), scrapeInterval, scrapeJob, resolution)); err != nil {
			return nil, err
		}
		if explicit, ok := rateWindowChoices()[spec.RateWindow]; ok && explicit != rateWindow {
			_ = log("warning", fmt.Sprintf("The configured %s rate window covers fewer than two scrapes; widened to %s.", spec.RateWindow, formatPromDuration(rateWindow)))
		}
	} else if err := log("info", fmt.Sprintf("Scrape interval unknown; using a %s rate window", formatPromDuration(rateWindow))); err != nil {
		return nil, err
	}
	definitions := deploymentMetricDefinitions(deployment)
	if spec.PeerOnly {
		definitions = ciliumMetricDefinitions(deployment, true)
	}
	definitions = applyRateWindow(scopeReusableNamespaceMetrics(definitions, deployment), rateWindow)
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
	// Run context and pod ownership are best effort and collected meanwhile;
	// their warnings are logged from this goroutine once they are done.
	type contextResult struct {
		context  *runContext
		pods     map[string]podWorkload
		warnings []string
	}
	contextResults := make(chan contextResult, 1)
	go func() {
		var warnings []string
		warn := func(message string) { warnings = append(warnings, message) }
		value := plugin.collectRunContext(queryContext, cluster, deployment, capability, endpoint, load, start, end, resolution, scrapeInterval, spec.PeerOnly, warn)
		pods := plugin.resolvePodWorkloads(queryContext, cluster, capability, endpoint, deployment.Spec.Namespace, start, end, rateWindow, nodeZones(value.Nodes), warn)
		contextResults <- contextResult{context: value, pods: pods, warnings: warnings}
	}()
	// The measured call chain is best effort as well; peers never build one
	// (multi-cluster datasets keep the primary cluster's model).
	type callModelResult struct {
		model    *callModel
		warnings []string
	}
	callModelResults := make(chan callModelResult, 1)
	go func() {
		var warnings []string
		var model *callModel
		if !spec.PeerOnly {
			model = plugin.collectCallModel(queryContext, cluster, deployment, capability, endpoint, load, scrapeInterval, func(message string) { warnings = append(warnings, message) })
		}
		callModelResults <- callModelResult{model: model, warnings: warnings}
	}()
	allSeries := []metricSeries{}
	catalog := []metricCatalogEntry{}
	if !spec.PeerOnly {
		allSeries = append(allSeries, configuredLoadSeries(load, start, end, resolution))
		catalog = append(catalog, configuredLoadCatalogEntry())
	}
	for range definitions {
		collected := <-results
		if collected.err != nil && !collected.definition.Optional {
			cancel()
			return nil, fmt.Errorf("collect %s: %w", collected.definition.ID, collected.err)
		}
		if len(collected.series) == 0 {
			if !collected.definition.Optional {
				cancel()
				return nil, fmt.Errorf("collect %s: metrics service returned no series", collected.definition.ID)
			}
			reason := "no series"
			if collected.err != nil {
				reason = collected.err.Error()
			}
			if err := log("info", fmt.Sprintf("Optional metric %s unavailable: %s", collected.definition.ID, reason)); err != nil {
				cancel()
				return nil, err
			}
			continue
		}
		if err := log("info", fmt.Sprintf("Collected %d %s series", len(collected.series), collected.definition.ID)); err != nil {
			cancel()
			return nil, err
		}
		allSeries = append(allSeries, collected.series...)
		catalog = append(catalog, collected.definition.catalogEntry())
	}
	if spec.SLOMilliseconds > 0 && !spec.PeerOnly {
		for _, series := range allSeries {
			if series.Metric == "load.p95_response_time" {
				allSeries = append(allSeries, sloViolationSeries(series, spec.SLOMilliseconds))
				for _, definition := range definitions {
					if definition.ID == series.Metric {
						catalog = append(catalog, sloCatalogEntry(definition, spec.SLOMilliseconds))
					}
				}
				break
			}
		}
	}
	controlStart := start
	if scope := deployment.Spec.ControlMetrics; scope != nil && !scope.StartedAt.IsZero() && scope.StartedAt.Before(start) && end.Sub(scope.StartedAt) <= 24*time.Hour {
		controlStart = scope.StartedAt.UTC().Truncate(time.Second)
	}
	controlSeries, controlSummaries, controlCatalog := plugin.collectControlMetrics(ctx, cluster, deployment, capability, endpoint, controlStart, end, resolution)
	allSeries = append(allSeries, controlSeries...)
	catalog = append(catalog, controlCatalog...)
	background := <-contextResults
	for _, warning := range background.warnings {
		_ = log("warning", warning)
	}
	modelResult := <-callModelResults
	for _, warning := range modelResult.warnings {
		_ = log("warning", warning)
	}
	labelWorkloads(allSeries, background.pods)
	sort.Slice(allSeries, func(i, j int) bool {
		if allSeries[i].Metric != allSeries[j].Metric {
			return allSeries[i].Metric < allSeries[j].Metric
		}
		return labelIdentity(allSeries[i].Labels) < labelIdentity(allSeries[j].Labels)
	})
	placement := plugin.collectPlacement(ctx, cluster, deployment, capability, endpoint, start, end, resolution)
	dataset := timeSeriesDataset{
		APIVersion: artifactAPI, Kind: "TimeSeriesDataset", Metadata: datasetMetadata{Name: deployment.Spec.Namespace + "/workload-overview", Version: "v1alpha1"},
		Spec: datasetSpec{
			Placement: placement, EndToEndSource: "locust", ApplicationRef: deployment.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: deployment.Spec.Namespace,
			Start: controlStart, WorkloadStartedAt: &start, ControlMetrics: controlSummaries, End: end, StepSeconds: int(resolution.Seconds()), SLOMilliseconds: spec.SLOMilliseconds,
			RateWindowSeconds: int(math.Ceil(rateWindow.Seconds())), ScrapeIntervalSeconds: int(math.Ceil(scrapeInterval.Seconds())),
			Metrics: buildCatalog(catalog, allSeries), RunContext: background.context,
			Source: datasetSource{Kind: capability.Spec.MetricsAPI, Version: capability.Metadata.Version, Profile: "kubephos-experiment-evidence/v2"},
			Series: allSeries, Summary: summarizeSeries(allSeries),
		},
	}
	if !spec.PeerOnly {
		dataset.Spec.LocustSummary = locustSummary(load)
		dataset.Spec.TaskMix = taskMix(load)
		dataset.Spec.CallModel = modelResult.model
	}
	return json.Marshal(result{Dataset: dataset})
}

// summarizeSeries counts the distinct metrics, series and samples present.
func summarizeSeries(series []metricSeries) datasetSummary {
	ids := map[string]bool{}
	samples := 0
	for _, item := range series {
		ids[item.Metric] = true
		samples += len(item.Points)
	}
	return datasetSummary{Metrics: len(ids), Series: len(series), Samples: samples}
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
	for index, step := range value.Spec.Steps {
		duration := float64(step.DurationSeconds)
		// The window end belongs to the last step: the profile is still running at
		// completedAt, so the final sample must not read as 0.
		if elapsed < cursor+duration || index == len(value.Spec.Steps)-1 && elapsed <= cursor+duration {
			local := math.Max(0, elapsed-cursor)
			switch step.Type {
			case "constant":
				return math.Max(0, step.RPS)
			case "sinusoidal":
				if step.PeriodSeconds <= 0 {
					return 0
				}
				baseline, amplitude := step.BaselineRPS, step.AmplitudeRPS
				// Same rule as the load generator: a range, when present, defines the wave.
				if step.MinRPS != nil && step.MaxRPS != nil {
					baseline, amplitude = (*step.MinRPS+*step.MaxRPS)/2, (*step.MaxRPS-*step.MinRPS)/2
				}
				return math.Max(0, baseline+amplitude*math.Sin(2*math.Pi*(local+float64(step.PhaseSeconds))/float64(step.PeriodSeconds)))
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

// serviceReporter selects server-side L7 measurements: sidecars report them as
// "destination" and Istio ambient waypoints as "waypoint". A namespace runs one
// data plane, so the two never count the same request twice.
const serviceReporter = `reporter=~"destination|waypoint"`

// KubePhos adds its own workloads to the application namespace: the
// pod-scheduling probe and the Locust load generator ("kubephos-load-<scenario>",
// see the load-session plugin). They are not part of the system under test, so
// application resource metrics exclude them. The pod pattern has no parentheses
// because namespace scoping rewrites rate() vectors that contain none.
// The Istio ambient waypoint Deployment is mesh data plane and stays included.
type workloadExclusion struct{ pod, deployment string }

var (
	applicationWorkloads = workloadExclusion{pod: `pod!~"kubephos-pod-scheduling-.*|kubephos-load-.*"`, deployment: `deployment!~"kubephos-pod-scheduling|kubephos-load-.*"`}
	// An external destination's namespace holds only the Locust driver, which
	// is then the only in-cluster workload these metrics can describe.
	externalDriverWorkloads = workloadExclusion{pod: `pod!~"kubephos-pod-scheduling-.*"`, deployment: `deployment!="kubephos-pod-scheduling"`}
)

func metricDefinitions(namespace, _ string) []metricDefinition {
	return namespaceMetricDefinitions(namespace, applicationWorkloads)
}

// Provenance vocabulary of the dataset catalog (spec.metrics).
const (
	sourceLocust           = "locust"
	sourceIstio            = "istio"
	sourceNginx            = "nginx"
	sourceCadvisor         = "cadvisor"
	sourceKubeStateMetrics = "kube-state-metrics"
	sourceMentat           = "mentat"
	sourceKubePhos         = "kubephos"
	sourceKubeScheduler    = "kube-scheduler"
	sourceDescheduler      = "descheduler"

	vantageClient        = "client"
	vantageServer        = "server"
	vantageResource      = "resource"
	vantageNetwork       = "network"
	vantageControlPlane  = "control-plane"
	vantageConfiguration = "configuration"

	collectorPrometheus = "prometheus"
	collectorKubePhos   = "kubephos"
)

const istioServerVantage = "measured by the Istio proxy in front of the destination workload (server side: sidecar \"destination\" reporter or ambient waypoint), per destination workload; excludes the client network path and load generator time"

func namespaceMetricDefinitions(namespace string, exclude workloadExclusion) []metricDefinition {
	selector := strconv.Quote(namespace)
	istio := `{` + serviceReporter + `,destination_workload_namespace=` + selector + `}`
	buckets := `sum by (destination_workload, le) (rate(istio_request_duration_milliseconds_bucket` + istio + rateWindowRange + `))`
	quantile := func(id, q, label string) metricDefinition {
		return metricDefinition{ID: id, Unit: "milliseconds", Aggregation: "mean", Source: sourceIstio, Vantage: vantageServer, Optional: q != "0.95",
			Query:       `histogram_quantile(` + q + `, ` + buckets + `)`,
			Description: label + " percentile of server response time " + istioServerVantage + ", estimated by interpolation inside the istio_request_duration_milliseconds histogram buckets."}
	}
	return append(locustMetricDefinitions(namespace), []metricDefinition{
		{ID: "application.request_rps", Unit: "requests_per_second", Aggregation: "sum", Source: sourceIstio, Vantage: vantageServer,
			Query:       `sum by (destination_workload) (rate(istio_requests_total` + istio + rateWindowRange + `))`,
			Description: "Rate of requests received by each workload, " + istioServerVantage + "."},
		quantile("application.p95_response_time", "0.95", "95th"),
		quantile("application.p50_response_time", "0.5", "50th (median)"),
		quantile("application.p90_response_time", "0.9", "90th"),
		quantile("application.p99_response_time", "0.99", "99th"),
		{ID: "application.mean_response_time", Unit: "milliseconds", Aggregation: "mean", Source: sourceIstio, Vantage: vantageServer, Optional: true,
			Query:       `sum by (destination_workload) (rate(istio_request_duration_milliseconds_sum` + istio + rateWindowRange + `)) / sum by (destination_workload) (rate(istio_request_duration_milliseconds_count` + istio + rateWindowRange + `))`,
			Description: "Mean server response time (rate of the latency sum divided by the rate of the request count) " + istioServerVantage + "."},
		{ID: "ingress.request_rps", Unit: "requests_per_second", Aggregation: "sum", Source: sourceNginx, Vantage: vantageServer, Optional: true,
			Query:       `sum(rate(nginx_http_requests_total{namespace=` + selector + `,` + exclude.pod + `}` + rateWindowRange + `))`,
			Description: "Rate of HTTP requests received by the application's nginx ingress proxies (stub_status counters read by nginx-prometheus-exporter), summed over all proxies: what reaches the entry point, including the redirects Locust follows within one request; absent when the application exposes no nginx counters."},
		{ID: "cpu.cores", Unit: "cores", Aggregation: "sum", Source: sourceCadvisor, Vantage: vantageResource,
			Query:       `sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=` + selector + `,container!="",container!="POD",` + exclude.pod + `}` + rateWindowRange + `))`,
			Description: "CPU used by each pod in cores: rate of container CPU seconds reported by cAdvisor (kubelet), summed over the pod's containers, excluding pause containers and KubePhos helper pods. Labels workload/workload_kind/node/zone are resolved from kube-state-metrics."},
		{ID: "memory.working_set", Unit: "bytes", Aggregation: "sum", Source: sourceCadvisor, Vantage: vantageResource,
			Query:       `sum by (pod) (container_memory_working_set_bytes{namespace=` + selector + `,container!="",container!="POD",` + exclude.pod + `})`,
			Description: "Working-set memory of each pod (cAdvisor container_memory_working_set_bytes summed over containers), sampled at each step; excludes pause containers and KubePhos helper pods."},
		{ID: "cpu.limit_cores", Unit: "cores", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource, Optional: true,
			Query:       `sum by (pod) (kube_pod_container_resource_limits{namespace=` + selector + `,resource="cpu",` + exclude.pod + `})`,
			Description: "Sum of the CPU limits (cores) of each pod's containers reported by kube-state-metrics. Containers without a limit are not counted, so a pod with any unlimited container is not CPU-capped by this value. Compare with cpu.cores to detect throttling."},
		{ID: "cpu.request_cores", Unit: "cores", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource, Optional: true,
			Query:       `sum by (pod) (kube_pod_container_resource_requests{namespace=` + selector + `,resource="cpu",` + exclude.pod + `})`,
			Description: "Sum of the CPU requests (cores) of each pod's containers reported by kube-state-metrics (what the scheduler reserves; autoscalers usually target a percentage of it)."},
		{ID: "pod.restarts", Unit: "count", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource,
			Query:       `sum by (pod) (kube_pod_container_status_restarts_total{namespace=` + selector + `,` + exclude.pod + `})`,
			Description: "Cumulative container restart counter of each pod reported by kube-state-metrics; the increase over the run is the number of restarts."},
		{ID: "deployment.ready_replicas", Unit: "replicas", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource,
			Query:       `max by (deployment) (kube_deployment_status_replicas_ready{namespace=` + selector + `,` + exclude.deployment + `})`,
			Description: "Ready replicas of each Deployment reported by kube-state-metrics (status.readyReplicas)."},
		{ID: "statefulset.ready_replicas", Unit: "replicas", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource, Optional: true,
			Query:       `max by (statefulset) (kube_statefulset_status_replicas_ready{namespace=` + selector + `})`,
			Description: "Ready replicas of each StatefulSet reported by kube-state-metrics (status.readyReplicas)."},
		daemonSetReadyDefinition(selector),
		{ID: "network.rtt", Unit: "milliseconds", Aggregation: "mean", Source: sourceMentat, Vantage: vantageNetwork,
			Query:       `1000 * sum by (origin_node, destination_node) (rate(node_latency_sum` + rateWindowRange + `)) / sum by (origin_node, destination_node) (rate(node_latency_count` + rateWindowRange + `))`,
			Description: "Mean ICMP round-trip time between each pair of Kubernetes nodes measured by the KubePhos mentat probe agent (rate of the latency sum over the rate of the probe count), in milliseconds."},
		{ID: "network.packet_loss", Unit: "percent", Aggregation: "mean", Source: sourceMentat, Vantage: vantageNetwork,
			Query:       `100 * avg_over_time(node_packet_loss_ratio` + rateWindowRange + `)`,
			Description: "Percentage of ICMP probes lost between each pair of Kubernetes nodes (mentat), averaged over the rate window."},
		{ID: "network.bandwidth", Unit: "bytes_per_second", Aggregation: "mean", Source: sourceMentat, Vantage: vantageNetwork,
			Query:       `node_bandwidth_bytes_per_second`,
			Description: "Link bandwidth between each pair of Kubernetes nodes estimated by mentat from UDP packet dispersion (latest probe value at each step)."},
	}...)
}

func daemonSetReadyDefinition(selector string) metricDefinition {
	return metricDefinition{ID: "daemonset.ready_replicas", Unit: "replicas", Aggregation: "sum", Source: sourceKubeStateMetrics, Vantage: vantageResource, Optional: true,
		Query:       `max by (daemonset) (kube_daemonset_status_number_ready{namespace=` + selector + `})`,
		Description: "Ready pods of each DaemonSet reported by kube-state-metrics (status.numberReady)."}
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

	if !deployment.Spec.External {
		return metricDefinitions(deployment.Spec.Namespace, trafficEntryPointMatcher(deployment))
	}
	definitions := namespaceMetricDefinitions(deployment.Spec.Namespace, externalDriverWorkloads)
	filtered := make([]metricDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if strings.HasPrefix(definition.ID, "application.") {
			continue
		}
		filtered = append(filtered, definition)
	}
	return filtered
}

const locustClientVantage = "measured by the Locust load generator (client side, end to end: includes the network path, the mesh and the application)"

// Locust measures all end-to-end requests at the load generator.
func locustMetricDefinitions(namespace string) []metricDefinition {
	selector := `namespace=` + strconv.Quote(namespace)
	requests := `kubephos_load_requests_total{` + selector
	success := `sum(rate(` + requests + `,outcome="success"}` + rateWindowRange + `))`
	failed := `sum(rate(` + requests + `,outcome="failure"}` + rateWindowRange + `))`
	total := `sum(rate(` + requests + `}` + rateWindowRange + `))`
	buckets := `sum by (le) (rate(kubephos_load_duration_milliseconds_bucket{` + selector + `}` + rateWindowRange + `))`
	quantile := func(id, q, label string) metricDefinition {
		return metricDefinition{ID: id, Unit: "milliseconds", Aggregation: "mean", Optional: q != "0.95",
			Query:       `histogram_quantile(` + q + `, ` + buckets + `)`,
			Description: label + " percentile of response time " + locustClientVantage + ", estimated by interpolation inside the kubephos_load_duration_milliseconds histogram buckets of each rate window; exact percentiles of the whole measured window are in locustSummary."}
	}
	definitions := []metricDefinition{
		{ID: "load.successful_rps", Unit: "requests_per_second", Aggregation: "sum", Query: success,
			Description: "Rate of requests that completed successfully, " + locustClientVantage + ", summed over all load workers."},
		{ID: "load.failed_rps", Unit: "requests_per_second", Aggregation: "sum", Query: failed,
			Description: "Rate of requests Locust recorded as failed (connection errors, timeouts, error status or failed response checks), summed over all load workers."},
		// Without traffic the ratio is undefined: 0/0 yields NaN, which is
		// dropped, so idle windows have no sample instead of a fake 0 %. The
		// "or 0 *" branch keeps 0 % when traffic exists but no failure series
		// was ever exported. An entirely idle run has no series at all.
		{ID: "load.failure_percentage", Unit: "percent", Aggregation: "mean", Optional: true,
			Query:       `100 * (` + failed + ` or 0 * ` + total + `) / ` + total,
			Description: "Failed requests as a percentage of all requests issued by Locust in each rate window (client side); no sample when no request was issued."},
		quantile("load.p95_response_time", "0.95", "95th"),
		quantile("load.p50_response_time", "0.5", "50th (median)"),
		quantile("load.p90_response_time", "0.9", "90th"),
		quantile("load.p99_response_time", "0.99", "99th"),
		{ID: "load.mean_response_time", Unit: "milliseconds", Aggregation: "mean", Optional: true,
			Query:       `sum(rate(kubephos_load_duration_milliseconds_sum{` + selector + `}` + rateWindowRange + `)) / sum(rate(kubephos_load_duration_milliseconds_count{` + selector + `}` + rateWindowRange + `))`,
			Description: "Mean response time " + locustClientVantage + ": rate of the latency sum divided by the rate of the request count."},
		{ID: "load.achieved_rps", Unit: "requests_per_second", Aggregation: "sum", Optional: true, Query: total,
			Description: "Total rate of requests actually issued and completed by Locust (successful + failed), summed over all load workers."},
		{ID: "load.offered_rps", Unit: "requests_per_second", Aggregation: "sum", Optional: true,
			Query:       `sum(kubephos_load_target_rps{` + selector + `})`,
			Description: "Request rate the load workers were trying to send at each step (sum of each worker's open-loop target, kubephos_load_target_rps); offered above achieved means the generator could not keep up."},
		{ID: "load.schedule_lag", Unit: "seconds", Aggregation: "mean", Optional: true,
			Query:       `sum(rate(kubephos_load_schedule_lag_seconds_sum{` + selector + `}` + rateWindowRange + `)) / sum(rate(kubephos_load_schedule_lag_seconds_count{` + selector + `}` + rateWindowRange + `))`,
			Description: "Mean delay per request between its scheduled send time and the moment Locust issued it, in seconds (coordinated-omission evidence: values near 0 mean the open-loop schedule was kept)."},
		{ID: "load.users", Unit: "count", Aggregation: "sum", Optional: true,
			Query:       `sum(kubephos_load_users{` + selector + `})`,
			Description: "Number of simulated Locust users running, summed over all load workers."},
	}
	for i := range definitions {
		definitions[i].Source, definitions[i].Vantage = sourceLocust, vantageClient
	}
	return definitions
}

// Native protocol latency includes Redis redirects, without an HTTP proxy.
func nativeMetricDefinitions(namespace string) []metricDefinition {
	selector := `namespace=` + strconv.Quote(namespace)
	requests := `kubephos_load_requests_total{` + selector
	definitions := append(locustMetricDefinitions(namespace), []metricDefinition{
		{ID: "redis.operation_rps", Unit: "requests_per_second", Aggregation: "sum", Source: sourceLocust, Vantage: vantageClient,
			Query:       `sum by (operation) (rate(` + requests + `}` + rateWindowRange + `))`,
			Description: "Rate of Redis operations issued by the Locust load generator (client side), per operation."},
		{ID: "redis.operation_p95_response_time", Unit: "milliseconds", Aggregation: "mean", Source: sourceLocust, Vantage: vantageClient,
			Query:       `histogram_quantile(0.95, sum by (operation, le) (rate(kubephos_load_duration_milliseconds_bucket{` + selector + `}` + rateWindowRange + `)))`,
			Description: "95th percentile of Redis operation latency per operation, measured by Locust (client side, includes cluster redirects), estimated from histogram buckets."},
	}...)
	for _, definition := range metricDefinitions(namespace, "") {
		if strings.HasPrefix(definition.ID, "load.") || strings.HasPrefix(definition.ID, "application.") || definition.ID == "statefulset.ready_replicas" {
			continue
		}
		if definition.ID == "deployment.ready_replicas" {
			// Native TCP applications run as StatefulSets; the id is kept for compatibility.
			definition.Query = `max by (statefulset) (kube_statefulset_status_replicas_ready{` + selector + `})`
			definition.Description = "Ready replicas of each application StatefulSet reported by kube-state-metrics (status.readyReplicas); native TCP applications run as StatefulSets."
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
	if dataset.Spec.RateWindowSeconds < 0 || dataset.Spec.RateWindowSeconds > 3600 || dataset.Spec.ScrapeIntervalSeconds < 0 {
		return errors.New("dataset rate window or scrape interval is invalid")
	}
	// expected holds the required metrics, allowed every metric this run may
	// contain; optional metrics may legitimately be absent.
	expected := map[string]bool{"load.configured_rps": false}
	allowed := map[string]bool{"load.configured_rps": true}
	if spec.SLOMilliseconds > 0 {
		expected["load.slo_violation_percentage"] = false
		allowed["load.slo_violation_percentage"] = true
	}
	for _, definition := range deploymentMetricDefinitions(deployment) {
		allowed[definition.ID] = true
		if !definition.Optional {
			expected[definition.ID] = false
		}
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
			allowed[summary.Metric] = true
		}
	}
	samples := 0
	for _, series := range dataset.Spec.Series {
		if !allowed[series.Metric] || series.Unit == "" || (series.Aggregation != "sum" && series.Aggregation != "mean") || len(series.Points) == 0 {
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
	if err := validateCallModel(dataset.Spec.CallModel); err != nil {
		return err
	}
	if err := validateCatalog(dataset.Spec.Metrics, dataset.Spec.Series); err != nil {
		return err
	}
	if dataset.Spec.Summary != summarizeSeries(dataset.Spec.Series) || dataset.Spec.Summary.Series != len(dataset.Spec.Series) || dataset.Spec.Summary.Samples != samples || samples == 0 {
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
