package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// callChainProfile is the optional LoadSession record of the call-chain
// profiling phase: each task ran alone at RPS during its window.
type callChainProfile struct {
	RPS            float64           `json:"rps"`
	SecondsPerTask int               `json:"secondsPerTask"`
	Windows        []callChainWindow `json:"windows"`
}

type callChainWindow struct {
	Task string    `json:"task"`
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// callModel is spec.callModel of the dataset: how many server-side requests
// each workload receives per execution of each Locust task.
type callModel struct {
	Source                string            `json:"source"`
	Description           string            `json:"description"`
	Method                string            `json:"method"`
	MeasuredAt            time.Time         `json:"measuredAt"`
	ApplicationRef        string            `json:"applicationRef"`
	ScenarioID            string            `json:"scenarioId"`
	RPS                   float64           `json:"rps,omitempty"`
	SecondsPerTask        int               `json:"secondsPerTask,omitempty"`
	WindowTrimSeconds     int               `json:"windowTrimSeconds"`
	StepSeconds           int               `json:"stepSeconds"`
	ScrapeIntervalSeconds int               `json:"scrapeIntervalSeconds,omitempty"`
	Proxies               []string          `json:"proxies,omitempty"`
	Queries               map[string]string `json:"queries,omitempty"`
	Tasks                 []callModelTask   `json:"tasks"`
}

type callModelTask struct {
	Task string `json:"task"`
	// From/To bound the counted interval: the window without its trim.
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	Executions    *float64  `json:"executions,omitempty"`
	EntryRequests float64   `json:"entryRequests"`
	// Services is per task execution and absent when the load generator does
	// not export kubephos_load_task_executions_total (older wrapper);
	// ServicesPerRequest is per HTTP request sent by Locust and always present
	// when Locust requests were counted.
	Services           map[string]float64 `json:"services,omitempty"`
	ServicesPerRequest map[string]float64 `json:"servicesPerRequest,omitempty"`
	Edges              []callModelEdge    `json:"edges,omitempty"`
}

type callModelEdge struct {
	Source       string   `json:"source"`
	Destination  string   `json:"destination"`
	PerExecution *float64 `json:"perExecution,omitempty"`
	PerRequest   *float64 `json:"perRequest,omitempty"`
}

const (
	callModelStep    = 5 * time.Second
	callModelMinTrim = 15 * time.Second
	callModelTimeout = 2 * time.Minute
)

// Mesh hops that forward requests rather than serve them: the KubePhos node
// proxy, gateway Deployments and the Istio ambient waypoint.
var callModelProxy = regexp.MustCompile(`^(?:node-proxy|gateway-.*|waypoint|.*-waypoint)$`)

const callModelDescription = "Measured call chain of the load scenario: each Locust task ran alone during the call-chain profiling phase; " +
	"requests received by each workload (Istio destination/waypoint reporter, server side, KubePhos workloads excluded) are divided by the task executions " +
	"(kubephos_load_task_executions_total) and by the HTTP requests sent by Locust (kubephos_load_requests_total) counted in the same interval. " +
	"Counts are differences of the cumulative counters summed per pod, sampled every stepSeconds from window start + windowTrimSeconds to window end, " +
	"with counter resets and series born in the interval counted from zero; no increase() extrapolation."

// callModelQueries returns the scoped instant-vector queries evaluated over
// each profiling window. Counters are summed per pod (not per series) to keep
// responses small while still detecting resets and replaced pods.
func callModelQueries(deployment applicationDeployment) (executions, requests, istio string) {
	selector := strconv.Quote(deployment.Spec.Namespace)
	definitions := []metricDefinition{
		{ID: "load.call_model_executions", Query: `sum by (task, pod) (kubephos_load_task_executions_total{namespace=` + selector + `})`},
		{ID: "load.call_model_requests", Query: `sum by (pod) (kubephos_load_requests_total{namespace=` + selector + `})`},
		{ID: "application.call_model_requests", Query: `sum by (source_workload, destination_workload, pod) (istio_requests_total{` + serviceReporter + `,destination_workload_namespace=` + selector + `,destination_workload!~"kubephos-load-.*|kubephos-pod-scheduling.*"})`},
	}
	definitions = scopeReusableNamespaceMetrics(definitions, deployment)
	return definitions[0].Query, definitions[1].Query, definitions[2].Query
}

// callModelTrim is the time dropped at the start of each window: the
// transition from the previous task (in-flight requests, the first scrape
// after the switch). At least one scrape interval and 15 s.
func callModelTrim(scrapeInterval time.Duration) time.Duration {
	trim := callModelMinTrim
	if scrapeInterval > trim {
		trim = scrapeInterval
	}
	return time.Duration(math.Ceil(trim.Seconds())) * time.Second
}

func parseCallChainProfile(load loadSession) (*callChainProfile, error) {
	raw := strings.TrimSpace(string(load.Spec.CallChainProfile))
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var profile callChainProfile
	if err := json.Unmarshal(load.Spec.CallChainProfile, &profile); err != nil {
		return nil, fmt.Errorf("invalid callChainProfile: %w", err)
	}
	if len(profile.Windows) == 0 {
		return nil, errors.New("callChainProfile has no windows")
	}
	for _, window := range profile.Windows {
		if window.Task == "" || !window.To.After(window.From) || window.To.Sub(window.From) > time.Hour {
			return nil, errors.New("callChainProfile has an invalid window")
		}
	}
	return &profile, nil
}

// collectCallModel builds spec.callModel from the profiling windows of the
// load session. It is best effort: any failure returns nil and a warning.
//
// Counting method. A window is 30-600 s and scrapes come every <= 30 s.
// increase(x[W]) evaluated at the window end would extrapolate each series
// from its first/last sample to the range boundaries (by up to 1.1 average
// intervals), so every counter is scaled by roughly W / sampled span. That
// factor is similar for Locust and Istio counters scraped at the same
// interval and largely cancels in the per-execution ratios, but the
// residual depends on each target's scrape phase, and increase() ignores the
// first sample of a series born inside the range (new edges, new response
// codes, pods of a fresh namespace), which biases rarely-called edges low.
// Instead each counter is read with query_range every 5 s from
// from + trim to to: count = sum of non-negative differences, a drop is a
// reset (the new value is added, as increase() does) and a group absent at
// the first step (or reappearing after a gap) is a new series counted from
// zero. The remaining error is the boundary quantisation of the scrapes,
// at most one scrape interval of traffic per boundary for each counter, i.e.
// a relative error of order scrape / (window - trim) that again mostly
// cancels in the ratios.
func (plugin Plugin) collectCallModel(ctx context.Context, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability, endpoint serviceEndpoint, load loadSession, scrapeInterval time.Duration, warn func(string)) *callModel {
	profile, err := parseCallChainProfile(load)
	if err != nil {
		warn("Call model omitted: " + err.Error())
		return nil
	}
	if profile == nil {
		return nil
	}
	if !istioApplicationMetrics(deployment) {
		warn("Call model omitted: the deployment has no Istio service metrics")
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, callModelTimeout)
	defer cancel()
	trim := callModelTrim(scrapeInterval)
	minimumSpan := scrapeInterval
	if minimumSpan < 2*callModelStep {
		minimumSpan = 2 * callModelStep
	}
	executionsQuery, requestsQuery, istioQuery := callModelQueries(deployment)
	model := callModel{
		Source: sourceIstio, Description: callModelDescription, Method: "counter-difference",
		ApplicationRef: load.Spec.ApplicationRef, ScenarioID: load.Spec.ScenarioID, RPS: profile.RPS, SecondsPerTask: profile.SecondsPerTask,
		WindowTrimSeconds: int(trim.Seconds()), StepSeconds: int(callModelStep.Seconds()), ScrapeIntervalSeconds: int(math.Ceil(scrapeInterval.Seconds())),
		Queries: map[string]string{"executions": executionsQuery, "entryRequests": requestsQuery, "requests": istioQuery},
		Tasks:   []callModelTask{},
	}
	proxies := map[string]bool{}
	measured := false
	for _, window := range profile.Windows {
		end := window.To.UTC().Truncate(time.Second)
		earliest := window.From.UTC().Add(trim)
		steps := int64(end.Sub(earliest) / callModelStep)
		start := end.Add(-time.Duration(steps) * callModelStep)
		if steps < 1 || end.Sub(start) < minimumSpan {
			warn(fmt.Sprintf("Call model: the %s window is too short to measure after a %s trim; task skipped", window.Task, trim))
			continue
		}
		query := func(promql string) ([]metricSeries, error) {
			path := rangeQueryPath(capability, endpoint, promql, start, end, callModelStep)
			raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+path)
			if err != nil {
				return nil, err
			}
			return decodeMatrix(raw, metricDefinition{ID: "call_model"}, start, end)
		}
		executionSeries, err := query(executionsQuery)
		if err != nil {
			warn("Call model omitted: task executions query failed: " + err.Error())
			return nil
		}
		requestSeries, err := query(requestsQuery)
		if err != nil {
			warn("Call model omitted: load generator requests query failed: " + err.Error())
			return nil
		}
		istioSeries, err := query(istioQuery)
		if err != nil {
			warn("Call model omitted: Istio requests query failed: " + err.Error())
			return nil
		}
		task := callModelTask{Task: window.Task, From: start, To: end}
		executions, found := 0.0, false
		for _, series := range executionSeries {
			if series.Labels["task"] == window.Task {
				executions += counterIncrease(series.Points, start)
				found = true
			}
		}
		for _, series := range requestSeries {
			task.EntryRequests += counterIncrease(series.Points, start)
		}
		task.EntryRequests = roundCount(task.EntryRequests)
		if found && executions > 0 {
			value := roundCount(executions)
			task.Executions = &value
		}
		services := map[string]float64{}
		edges := map[[2]string]float64{}
		for _, series := range istioSeries {
			count := counterIncrease(series.Points, start)
			destination := series.Labels["destination_workload"]
			if destination == "" || count <= 0 {
				continue
			}
			services[destination] += count
			source := series.Labels["source_workload"]
			if source == "" {
				source = "unknown"
			}
			if strings.HasPrefix(source, "kubephos-load-") || strings.HasPrefix(source, "kubephos-pod-scheduling") {
				continue
			}
			edges[[2]string{source, destination}] += count
		}
		if task.Executions == nil && task.EntryRequests <= 0 {
			warn(fmt.Sprintf("Call model: no task executions or Locust requests counted for %s; task skipped", window.Task))
			continue
		}
		ratio := func(count, basis float64) *float64 {
			if basis <= 0 {
				return nil
			}
			value := roundRatio(count / basis)
			return &value
		}
		for name, count := range services {
			if callModelProxy.MatchString(name) {
				proxies[name] = true
			}
			if task.Executions != nil {
				if task.Services == nil {
					task.Services = map[string]float64{}
				}
				task.Services[name] = *ratio(count, *task.Executions)
			}
			if task.EntryRequests > 0 {
				if task.ServicesPerRequest == nil {
					task.ServicesPerRequest = map[string]float64{}
				}
				task.ServicesPerRequest[name] = *ratio(count, task.EntryRequests)
			}
		}
		for key, count := range edges {
			for _, name := range key {
				if callModelProxy.MatchString(name) {
					proxies[name] = true
				}
			}
			edge := callModelEdge{Source: key[0], Destination: key[1], PerRequest: ratio(count, task.EntryRequests)}
			if task.Executions != nil {
				edge.PerExecution = ratio(count, *task.Executions)
			}
			task.Edges = append(task.Edges, edge)
		}
		sort.Slice(task.Edges, func(i, j int) bool {
			if task.Edges[i].Source != task.Edges[j].Source {
				return task.Edges[i].Source < task.Edges[j].Source
			}
			return task.Edges[i].Destination < task.Edges[j].Destination
		})
		if len(services) > 0 {
			measured = true
		}
		model.Tasks = append(model.Tasks, task)
		if end.After(model.MeasuredAt) {
			model.MeasuredAt = end
		}
	}
	if !measured {
		warn("Call model omitted: no server-side requests were measured in the profiling windows")
		return nil
	}
	for name := range proxies {
		model.Proxies = append(model.Proxies, name)
	}
	sort.Strings(model.Proxies)
	return &model
}

// istioApplicationMetrics reports whether this deployment collects Istio
// server-side metrics (not Cilium, native TCP or an external destination).
func istioApplicationMetrics(deployment applicationDeployment) bool {
	for _, definition := range deploymentMetricDefinitions(deployment) {
		if definition.ID == "application.request_rps" {
			return true
		}
	}
	return false
}

// counterIncrease counts the events of a cumulative counter sampled at
// regular steps from start: positive differences, a drop is a reset (the new
// value counts from zero) and a series absent at start or reappearing after a
// gap is new, counted from zero.
func counterIncrease(points []metricPoint, start time.Time) float64 {
	total := 0.0
	for index, point := range points {
		if index == 0 {
			if point.Timestamp.After(start) {
				total += point.Value
			}
			continue
		}
		previous := points[index-1]
		switch {
		case point.Timestamp.Sub(previous.Timestamp) > callModelStep*3/2:
			total += point.Value
		case point.Value >= previous.Value:
			total += point.Value - previous.Value
		default:
			total += point.Value
		}
	}
	return total
}

func roundCount(value float64) float64 {
	return math.Round(value*1000) / 1000
}

// roundRatio keeps six significant digits.
func roundRatio(value float64) float64 {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	parsed, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'g', 6, 64), 64)
	return parsed
}

// taskMix returns the LoadSession task mix verbatim, or nil when absent.
func taskMix(load loadSession) json.RawMessage {
	raw := strings.TrimSpace(string(load.Spec.TaskMix))
	if raw == "" || raw == "null" {
		return nil
	}
	return load.Spec.TaskMix
}

func validateCallModel(model *callModel) error {
	if model == nil {
		return nil
	}
	invalid := errors.New("dataset call model is invalid")
	if model.Source == "" || len(model.Tasks) == 0 || model.WindowTrimSeconds < 0 {
		return invalid
	}
	finite := func(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 }
	for _, task := range model.Tasks {
		if task.Task == "" || !finite(task.EntryRequests) || task.Executions != nil && !finite(*task.Executions) {
			return invalid
		}
		for _, values := range []map[string]float64{task.Services, task.ServicesPerRequest} {
			for name, value := range values {
				if name == "" || !finite(value) {
					return invalid
				}
			}
		}
		for _, edge := range task.Edges {
			if edge.Source == "" || edge.Destination == "" || edge.PerExecution != nil && !finite(*edge.PerExecution) || edge.PerRequest != nil && !finite(*edge.PerRequest) {
				return invalid
			}
		}
	}
	return nil
}
