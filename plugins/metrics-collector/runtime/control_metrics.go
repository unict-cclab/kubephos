package metricscollector

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

type controlMetricsScope struct {
	PodScheduling bool      `json:"podScheduling,omitempty"`
	StartedAt     time.Time `json:"startedAt"`
	SchedulerName string    `json:"schedulerName"`
	Namespace     string    `json:"namespace,omitempty"`
}

type controlMetricSummary struct {
	TimestampResolutionSeconds *int     `json:"timestampResolutionSeconds,omitempty"`
	Metric                     string   `json:"metric"`
	Status                     string   `json:"status"`
	Scope                      string   `json:"scope"`
	Source                     string   `json:"source,omitempty"`
	Count                      *float64 `json:"count,omitempty"`
	Mean                       *float64 `json:"mean,omitempty"`
	Total                      *float64 `json:"total,omitempty"`
	Reason                     string   `json:"reason,omitempty"`
}

func isControlMetric(metric string) bool {
	return metric == "scheduler.pod_scheduling_latency" || metric == "scheduler.cycle_duration" || metric == "descheduler.cycle_duration" || metric == "descheduler.evictions"
}

// Read native counters at the requested data resolution. Differences yield a mean
// duration of cycles completed between observations, not a percentile of durations.
// Preserve per-process labels until after reset handling; never difference summed counters.
func (plugin Plugin) collectControlMetrics(ctx context.Context, cluster clusterConnection, deployment applicationDeployment, capability observabilityCapability, endpoint serviceEndpoint, start, end time.Time, resolution time.Duration) ([]metricSeries, []controlMetricSummary) {
	scope := deployment.Spec.ControlMetrics
	if scope == nil {
		return nil, nil
	}
	schedulerJob := `job="apiserver"`
	schedulerScope := "cluster"
	fresh := false
	if scope.SchedulerName != "default-scheduler" && scope.SchedulerName != "" {
		schedulerJob = "job=" + strconv.Quote(scope.Namespace+"/scheduler-metrics")
		schedulerScope = "experiment"
		fresh = true
	}
	type component struct {
		metric, selector, base, legacy, scope string
		fresh                                 bool
	}
	components := []component{{"scheduler.cycle_duration", schedulerJob, "scheduler_scheduling_algorithm_duration_seconds", "", schedulerScope, fresh}}
	var summaries []controlMetricSummary
	if scope.Namespace != "" {
		components = append(components, component{"descheduler.cycle_duration", "job=" + strconv.Quote(scope.Namespace+"/descheduler-metrics"), "descheduler_loop_duration_seconds", "descheduler_descheduler_loop_duration_seconds", "experiment", true})
	} else {
		summaries = append(summaries, controlMetricSummary{Metric: "descheduler.cycle_duration", Status: "not_configured", Scope: "experiment"}, controlMetricSummary{Metric: "descheduler.evictions", Status: "not_configured", Scope: "experiment"})
	}
	read := func(query string) ([]metricSeries, error) {
		raw, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+rangeQueryPath(capability, endpoint, query, start, end, resolution))
		if err != nil {
			return nil, err
		}
		definition := metricDefinition{ID: "counter", Unit: "count", Aggregation: "sum"}
		series, err := decodeMatrix(raw, definition, start, end)
		if err != nil {
			return nil, err
		}
		if end.Sub(start)%resolution != 0 {
			// query_range steps need not land on the exact end of a run.
			tailRaw, tailErr := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, "get", "--raw="+rangeQueryPath(capability, endpoint, query, end, end, resolution))
			if tailErr != nil {
				return nil, tailErr
			}
			tail, tailErr := decodeMatrix(tailRaw, definition, end, end)
			if tailErr != nil {
				return nil, tailErr
			}
			positions := map[string]int{}
			for i, source := range series {
				positions[labelIdentity(source.Labels)] = i
			}
			for _, source := range tail {
				key := labelIdentity(source.Labels)
				if index, ok := positions[key]; ok {
					series[index].Points = append(series[index].Points, source.Points...)
				} else {
					series = append(series, source)
				}
			}
		}
		return series, nil
	}
	counterQuery := func(base, legacy, selector, suffix string) string {
		query := `sum by (instance,pod) (` + base + suffix + `{` + selector + `})`
		if legacy != "" {
			query += ` or sum by (instance,pod) (` + legacy + suffix + `{` + selector + `})`
		}
		return query
	}
	var series []metricSeries
	if scope.PodScheduling {
		summary := controlMetricSummary{Metric: "scheduler.pod_scheduling_latency", Status: "unavailable", Scope: "experiment", Source: "kubernetes-pod-conditions"}
		latencies, latencyErr := read(podSchedulingQuery("kubephos_pod_scheduling_latency_milliseconds", deployment.Spec.Namespace))
		scheduled, scheduledErr := read(podSchedulingQuery("kubephos_pod_scheduling_scheduled_timestamp_seconds", deployment.Spec.Namespace))
		observed, observedErr := read(podSchedulingQuery("kubephos_pod_scheduling_observed_pods", deployment.Spec.Namespace))
		if latencyErr == nil && scheduledErr == nil && observedErr == nil && len(observed) > 0 {
			var podSeries []metricSeries
			podSeries, summary = podSchedulingSeries(latencies, scheduled, start, end, resolution)
			series = append(series, podSeries...)
			maximumObserved := 0.0
			for _, item := range observed {
				for _, point := range item.Points {
					if point.Value > maximumObserved {
						maximumObserved = point.Value
					}
				}
			}
			if maximumObserved > float64(len(podSeries)) {
				summary.Status = "unavailable"
				summary.Count = nil
				summary.Mean = nil
				summary.Reason = "Pod scheduling observations are incomplete"
				series = series[:len(series)-len(podSeries)]
			}
		} else {
			summary.Reason = "Pod scheduling observer metrics unavailable"
		}
		summaries = append(summaries, summary)
	}

	for _, component := range components {
		summary := controlMetricSummary{Metric: component.metric, Status: "unavailable", Scope: component.scope, Source: component.base}
		sums, err := read(counterQuery(component.base, component.legacy, component.selector, "_sum"))
		if err != nil {
			summary.Reason = "Duration metrics query failed"
			summaries = append(summaries, summary)
			continue
		}
		counts, err := read(counterQuery(component.base, component.legacy, component.selector, "_count"))
		if err != nil || len(sums) == 0 || len(counts) == 0 {
			summary.Reason = "Native duration metrics unavailable"
			summaries = append(summaries, summary)
			continue
		}
		durationSeries, total, count := durationCounterSeries(component.metric, sums, counts, component.fresh, component.scope)
		summary.Count = &count
		summary.Status = "no_events"
		if count > 0 {
			mean := 1000 * total / count
			summary.Mean = &mean
			summary.Status = "available"
			series = append(series, durationSeries)
		}
		summaries = append(summaries, summary)
	}
	if scope.Namespace != "" {
		summary := controlMetricSummary{Metric: "descheduler.evictions", Status: "unavailable", Scope: "experiment", Source: "descheduler_pods_evicted_total"}
		// namespace on the metric is renamed by Prometheus because the scrape target
		// already has its own namespace label. Select only successful app evictions.
		selector := `job=` + strconv.Quote(scope.Namespace+"/descheduler-metrics") + `,exported_namespace=` + strconv.Quote(deployment.Spec.Namespace) + `,result="success"`
		counters, err := read(counterQuery("descheduler_pods_evicted_total", "descheduler_pods_evicted", selector, ""))
		if err == nil && len(counters) > 0 {
			points, total := cumulativeCounterSeries(counters, true, start)
			summary.Total = &total
			summary.Status = "available"
			series = append(series, metricSeries{Metric: summary.Metric, Unit: "count", Aggregation: "sum", Labels: map[string]string{"scope": "experiment"}, Points: points})
		} else {
			summary.Reason = "Successful eviction counter unavailable"
		}
		summaries = append(summaries, summary)
	}
	return series, summaries
}

func counterChanges(series []metricSeries, fresh bool) map[time.Time]float64 {
	changes := map[time.Time]float64{}
	for _, s := range series {
		previous := 0.0
		for index, point := range s.Points {
			if point.Value < 0 || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				continue
			}
			delta := point.Value - previous
			if index == 0 && !fresh {
				delta = 0
			} else if delta < 0 {
				delta = point.Value
			}
			changes[point.Timestamp] += delta
			previous = point.Value
		}
	}
	return changes
}

func orderedTimes(values map[time.Time]float64) []time.Time {
	times := make([]time.Time, 0, len(values))
	for timestamp := range values {
		times = append(times, timestamp)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	return times
}

func durationCounterSeries(metric string, sums, counts []metricSeries, fresh bool, scope string) (metricSeries, float64, float64) {
	// Match sum/count for each source and timestamp before aggregation. A missing
	// sum or count must never create a synthetic zero-duration cycle.
	sumBySource := map[string]map[time.Time]float64{}
	for _, source := range sums {
		values := map[time.Time]float64{}
		for _, point := range source.Points {
			values[point.Timestamp] = point.Value
		}
		sumBySource[labelIdentity(source.Labels)] = values
	}
	sumChanges, countChanges := map[time.Time]float64{}, map[time.Time]float64{}
	for _, source := range counts {
		matching, ok := sumBySource[labelIdentity(source.Labels)]
		if !ok {
			continue
		}
		previousCount, previousSum := 0.0, 0.0
		seen := false
		for _, point := range source.Points {
			duration, exists := matching[point.Timestamp]
			if !exists {
				continue
			}
			if duration < 0 || point.Value < 0 {
				continue
			}
			countDelta, sumDelta := point.Value-previousCount, duration-previousSum
			if !seen && !fresh {
				countDelta, sumDelta = 0, 0
			} else if countDelta < 0 || sumDelta < 0 {
				countDelta, sumDelta = point.Value, duration
			}
			sumChanges[point.Timestamp] += sumDelta
			countChanges[point.Timestamp] += countDelta
			previousCount, previousSum, seen = point.Value, duration, true
		}
	}
	result := metricSeries{Metric: metric, Unit: "milliseconds", Aggregation: "mean", Labels: map[string]string{"scope": scope}, Points: []metricPoint{}}
	total, count := 0.0, 0.0
	for _, timestamp := range orderedTimes(countChanges) {
		n := countChanges[timestamp]
		if n <= 0 {
			continue
		}
		duration := sumChanges[timestamp]
		total += duration
		count += n
		result.Points = append(result.Points, metricPoint{Timestamp: timestamp, Value: 1000 * duration / n})
	}
	return result, total, count
}

func cumulativeCounterSeries(series []metricSeries, fresh bool, start time.Time) ([]metricPoint, float64) {
	changes := counterChanges(series, fresh)
	points := []metricPoint{}
	if _, exists := changes[start]; !exists {
		points = append(points, metricPoint{Timestamp: start, Value: 0})
	}
	total := 0.0
	for _, timestamp := range orderedTimes(changes) {
		total += changes[timestamp]
		points = append(points, metricPoint{Timestamp: timestamp, Value: total})
	}
	return points, total
}

func validateControlSummary(summary controlMetricSummary) error {
	if summary.TimestampResolutionSeconds != nil && *summary.TimestampResolutionSeconds <= 0 {
		return fmt.Errorf("invalid timestamp resolution")
	}
	if !isControlMetric(summary.Metric) || (summary.Scope != "cluster" && summary.Scope != "experiment") {
		return fmt.Errorf("invalid control metric scope")
	}
	switch summary.Status {
	case "available", "no_events", "unavailable", "not_configured":
	default:
		return fmt.Errorf("invalid control metric status")
	}
	for _, value := range []*float64{summary.Count, summary.Mean, summary.Total} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0) {
			return fmt.Errorf("invalid control metric summary")
		}
	}
	if summary.Status == "available" && (summary.Metric == "descheduler.evictions" && summary.Total == nil || summary.Metric != "descheduler.evictions" && (summary.Count == nil || *summary.Count <= 0 || summary.Mean == nil)) {
		return fmt.Errorf("incomplete control metric summary")
	}
	return nil
}
