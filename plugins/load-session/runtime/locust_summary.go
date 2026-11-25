package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"sync"
	"time"
)

// locustSummary is the exact Locust statistics of the measured window, merged over
// every worker. Percentiles come from the merged response_times histograms using
// Locust's own algorithm, never from averaging per-worker percentiles.
type locustSummary struct {
	Workers      int                     `json:"workers"`
	MeasuredFrom *time.Time              `json:"measuredFrom,omitempty"`
	MeasuredTo   *time.Time              `json:"measuredTo,omitempty"`
	Requests     int64                   `json:"requests"`
	Failures     int64                   `json:"failures"`
	Endpoints    []locustEndpointSummary `json:"endpoints"`
	Total        locustEndpointSummary   `json:"total"`
	Errors       []locustErrorSummary    `json:"errors"`
	WorkerCPU    []locustWorkerCPU       `json:"workerCpu"`
	Schedule     locustScheduleSummary   `json:"schedule"`
}

type locustEndpointSummary struct {
	Method   string  `json:"method"`
	Name     string  `json:"name"`
	Requests int64   `json:"requests"`
	Failures int64   `json:"failures"`
	MeanMs   float64 `json:"meanMs"`
	MinMs    float64 `json:"minMs"`
	MaxMs    float64 `json:"maxMs"`
	P50Ms    float64 `json:"p50Ms"`
	P90Ms    float64 `json:"p90Ms"`
	P95Ms    float64 `json:"p95Ms"`
	P99Ms    float64 `json:"p99Ms"`
	P999Ms   float64 `json:"p999Ms"`
}

type locustErrorSummary struct {
	Method      string `json:"method"`
	Name        string `json:"name"`
	Error       string `json:"error"`
	Occurrences int64  `json:"occurrences"`
}

type locustWorkerCPU struct {
	Worker     string  `json:"worker"`
	MaxPercent float64 `json:"maxPercent"`
	Warnings   int64   `json:"warnings"`
}

type locustScheduleSummary struct {
	LateRequests   int64   `json:"lateRequests"`
	MaxLagSeconds  float64 `json:"maxLagSeconds"`
	MeanLagSeconds float64 `json:"meanLagSeconds"`
}

// workerSummary is the JSON served by the Locust wrapper on :9646/stats/summary.
type workerSummary struct {
	Phase        *int               `json:"phase"`
	MeasuredFrom *float64           `json:"measuredFrom"`
	Entries      []workerStatsEntry `json:"entries"`
	Total        *workerStatsEntry  `json:"total"`
	Errors       []workerStatsError `json:"errors"`
	CPU          struct {
		MaxPercent float64 `json:"maxPercent"`
		Warnings   int64   `json:"warnings"`
	} `json:"cpu"`
	Schedule struct {
		Count  int64   `json:"count"`
		LagSum float64 `json:"lagSum"`
		Late   int64   `json:"late"`
		MaxLag float64 `json:"maxLag"`
	} `json:"schedule"`
	// TaskMix is the effective request mix the worker applied (same on every worker).
	TaskMix []taskMixEntry `json:"taskMix,omitempty"`
}

type workerStatsEntry struct {
	Method            string           `json:"method"`
	Name              string           `json:"name"`
	NumRequests       int64            `json:"numRequests"`
	NumNoneRequests   int64            `json:"numNoneRequests"`
	NumFailures       int64            `json:"numFailures"`
	TotalResponseTime float64          `json:"totalResponseTime"`
	MinResponseTime   *float64         `json:"minResponseTime"`
	MaxResponseTime   float64          `json:"maxResponseTime"`
	ResponseTimes     map[string]int64 `json:"responseTimes"`
}

type workerStatsError struct {
	Method      string `json:"method"`
	Name        string `json:"name"`
	Error       string `json:"error"`
	Occurrences int64  `json:"occurrences"`
}

type namedWorkerSummary struct {
	worker  string
	summary workerSummary
}

// locustSummaryScript reads the summary from inside the Locust container, so the
// request neither crosses a service mesh nor needs pods/proxy permissions.
const locustSummaryScript = `import sys, urllib.request
sys.stdout.write(urllib.request.urlopen("http://127.0.0.1:9646/stats/summary", timeout=20).read().decode())`

// collectLocustSummary fetches every worker's statistics while the workers drain
// and merges them. Any missing worker invalidates the whole summary. The effective
// task mix reported by the workers is returned even when the merge fails.
func collectLocustSummary(ctx context.Context, runner commandRunner, kubeconfig, namespace string, profile loadProfile, replicas int, from, to time.Time) (*locustSummary, []taskMixEntry, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "json")
	if err != nil {
		return nil, nil, fmt.Errorf("list load workers: %w", err)
	}
	var pods struct {
		Items []timelinePod `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &pods) != nil || len(pods.Items) != replicas {
		return nil, nil, errors.New("the load worker set is incomplete")
	}
	var mu sync.Mutex
	collected := []namedWorkerSummary{}
	err = parallelTimelinePods(ctx, pods.Items, func(ctx context.Context, pod timelinePod) error {
		value, err := fetchWorkerSummary(ctx, runner, kubeconfig, namespace, pod.Metadata.Name)
		if err != nil {
			return fmt.Errorf("worker %s: %w", pod.Metadata.Name, err)
		}
		mu.Lock()
		collected = append(collected, namedWorkerSummary{worker: pod.Metadata.Name, summary: value})
		mu.Unlock()
		return nil
	})
	var taskMix []taskMixEntry
	sort.Slice(collected, func(left, right int) bool { return collected[left].worker < collected[right].worker })
	for _, worker := range collected {
		if len(worker.summary.TaskMix) > 0 {
			taskMix = worker.summary.TaskMix
			break
		}
	}
	if err != nil {
		return nil, taskMix, err
	}
	merged, err := mergeLocustSummaries(collected, from, to)
	return merged, taskMix, err
}

func fetchWorkerSummary(ctx context.Context, runner commandRunner, kubeconfig, namespace, pod string) (workerSummary, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", namespace, pod, "-c", "locust", "--", "python3", "-c", locustSummaryScript)
	if err != nil {
		// Images without python3 on PATH: go through the API server pod proxy.
		raw, err = runner.Run(ctx, kubeconfig, nil, "get", "--raw=/api/v1/namespaces/"+namespace+"/pods/"+pod+":9646/proxy/stats/summary")
		if err != nil {
			return workerSummary{}, err
		}
	}
	var value workerSummary
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return workerSummary{}, errors.New("statistics summary is not valid JSON")
	}
	return value, nil
}

type mergedEntry struct {
	method, name             string
	requests, none, failures int64
	totalTime, maxTime       float64
	minTime                  *float64
	responseTimes            map[float64]int64
}

func (entry *mergedEntry) add(value workerStatsEntry) error {
	entry.requests += value.NumRequests
	entry.none += value.NumNoneRequests
	entry.failures += value.NumFailures
	entry.totalTime += value.TotalResponseTime
	entry.maxTime = math.Max(entry.maxTime, value.MaxResponseTime)
	if value.MinResponseTime != nil && (entry.minTime == nil || *value.MinResponseTime < *entry.minTime) {
		minimum := *value.MinResponseTime
		entry.minTime = &minimum
	}
	if entry.responseTimes == nil {
		entry.responseTimes = map[float64]int64{}
	}
	for key, count := range value.ResponseTimes {
		bucket, err := strconv.ParseFloat(key, 64)
		if err != nil || math.IsNaN(bucket) || math.IsInf(bucket, 0) || count < 0 {
			return fmt.Errorf("invalid response time bucket %q", key)
		}
		entry.responseTimes[bucket] += count
	}
	return nil
}

func (entry *mergedEntry) summary() locustEndpointSummary {
	result := locustEndpointSummary{Method: entry.method, Name: entry.name, Requests: entry.requests, Failures: entry.failures, MaxMs: entry.maxTime}
	if measured := entry.requests - entry.none; measured > 0 {
		result.MeanMs = entry.totalTime / float64(measured)
	}
	if entry.minTime != nil {
		result.MinMs = *entry.minTime
	}
	result.P50Ms = locustPercentile(entry.responseTimes, entry.requests, 0.5)
	result.P90Ms = locustPercentile(entry.responseTimes, entry.requests, 0.9)
	result.P95Ms = locustPercentile(entry.responseTimes, entry.requests, 0.95)
	result.P99Ms = locustPercentile(entry.responseTimes, entry.requests, 0.99)
	result.P999Ms = locustPercentile(entry.responseTimes, entry.requests, 0.999)
	return result
}

// locustPercentile is locust.stats.calculate_response_time_percentile: walk the
// rounded response times from the slowest down until the remaining requests are
// within the requested share.
func locustPercentile(responseTimes map[float64]int64, requests int64, percent float64) float64 {
	threshold := int64(float64(requests) * percent)
	keys := make([]float64, 0, len(responseTimes))
	for key := range responseTimes {
		keys = append(keys, key)
	}
	sort.Sort(sort.Reverse(sort.Float64Slice(keys)))
	processed := int64(0)
	for _, key := range keys {
		processed += responseTimes[key]
		if requests-processed <= threshold {
			return key
		}
	}
	return 0
}

func mergeLocustSummaries(workers []namedWorkerSummary, from, to time.Time) (*locustSummary, error) {
	if len(workers) == 0 {
		return nil, errors.New("no worker statistics")
	}
	sort.Slice(workers, func(left, right int) bool { return workers[left].worker < workers[right].worker })
	endpoints := map[[2]string]*mergedEntry{}
	total := &mergedEntry{name: "Aggregated"}
	errorsByKey := map[[3]string]int64{}
	result := &locustSummary{Workers: len(workers), MeasuredFrom: &from, MeasuredTo: &to, Endpoints: []locustEndpointSummary{}, Errors: []locustErrorSummary{}, WorkerCPU: []locustWorkerCPU{}}
	lagSum, lagCount := 0.0, int64(0)
	for _, worker := range workers {
		value := worker.summary
		// Each worker resets its statistics at the shared t=0; a different reset
		// instant means its numbers cover another window.
		if value.MeasuredFrom == nil || math.Abs(*value.MeasuredFrom-float64(from.UnixNano())/1e9) > 1 {
			return nil, fmt.Errorf("worker %s did not measure from the shared start", worker.worker)
		}
		if value.Total == nil {
			return nil, fmt.Errorf("worker %s has no aggregated statistics", worker.worker)
		}
		for _, entry := range value.Entries {
			key := [2]string{entry.Method, entry.Name}
			merged := endpoints[key]
			if merged == nil {
				merged = &mergedEntry{method: entry.Method, name: entry.Name}
				endpoints[key] = merged
			}
			if err := merged.add(entry); err != nil {
				return nil, err
			}
		}
		if err := total.add(*value.Total); err != nil {
			return nil, err
		}
		for _, item := range value.Errors {
			errorsByKey[[3]string{item.Method, item.Name, item.Error}] += item.Occurrences
		}
		result.WorkerCPU = append(result.WorkerCPU, locustWorkerCPU{Worker: worker.worker, MaxPercent: value.CPU.MaxPercent, Warnings: value.CPU.Warnings})
		result.Schedule.LateRequests += value.Schedule.Late
		result.Schedule.MaxLagSeconds = math.Max(result.Schedule.MaxLagSeconds, value.Schedule.MaxLag)
		lagSum += value.Schedule.LagSum
		lagCount += value.Schedule.Count
	}
	if lagCount > 0 {
		result.Schedule.MeanLagSeconds = lagSum / float64(lagCount)
	}
	for _, entry := range endpoints {
		if entry.requests == 0 && entry.failures == 0 {
			continue
		}
		result.Endpoints = append(result.Endpoints, entry.summary())
	}
	sort.Slice(result.Endpoints, func(left, right int) bool {
		if result.Endpoints[left].Name == result.Endpoints[right].Name {
			return result.Endpoints[left].Method < result.Endpoints[right].Method
		}
		return result.Endpoints[left].Name < result.Endpoints[right].Name
	})
	result.Total = total.summary()
	result.Requests, result.Failures = result.Total.Requests, result.Total.Failures
	for key, occurrences := range errorsByKey {
		result.Errors = append(result.Errors, locustErrorSummary{Method: key[0], Name: key[1], Error: key[2], Occurrences: occurrences})
	}
	sort.Slice(result.Errors, func(left, right int) bool {
		a, b := result.Errors[left], result.Errors[right]
		if a.Occurrences != b.Occurrences {
			return a.Occurrences > b.Occurrences
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Method != b.Method {
			return a.Method < b.Method
		}
		return a.Error < b.Error
	})
	return result, nil
}
