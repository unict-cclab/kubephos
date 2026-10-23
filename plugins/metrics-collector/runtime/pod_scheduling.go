package metricscollector

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// Each retained UID contributes once, independent of how many times Prometheus
// scraped it. Recreated StatefulSet pods have different UIDs and stay distinct.
func podSchedulingSeries(latencies, scheduled []metricSeries, start, end time.Time, resolution time.Duration) ([]metricSeries, controlMetricSummary) {
	precision := 1
	summary := controlMetricSummary{Metric: "scheduler.pod_scheduling_latency", Scope: "experiment", Status: "no_events", Source: "kubernetes-pod-conditions", TimestampResolutionSeconds: &precision}
	times := map[string]float64{}
	for _, series := range scheduled {
		for _, point := range series.Points {
			times[series.Labels["pod_uid"]] = point.Value
		}
	}
	byUID := map[string]metricSeries{}
	values := map[string]float64{}
	for _, series := range latencies {
		uid := series.Labels["pod_uid"]
		seconds, ok := times[uid]
		if uid == "" || !ok || series.Labels["workload"] == "" || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
			continue
		}
		completion := time.Unix(0, int64(seconds*1e9)).UTC()
		if completion.Before(start) || completion.After(end) {
			continue
		}
		for _, point := range series.Points {
			if point.Value < 0 || math.IsNaN(point.Value) || math.IsInf(point.Value, 0) {
				continue
			}
			elapsed := completion.Sub(start)
			offset := (elapsed + resolution - 1) / resolution * resolution
			at := start.Add(offset)
			if at.After(end) {
				at = end
			}
			labels := map[string]string{"scope": "experiment", "pod_uid": uid, "workload": series.Labels["workload"], "workload_pod": series.Labels["workload_pod"], "scheduled_at": completion.Format(time.RFC3339Nano), "timestamp_resolution_seconds": "1"}
			byUID[uid] = metricSeries{Metric: summary.Metric, Unit: "milliseconds", Aggregation: "mean", Labels: labels, Points: []metricPoint{{Timestamp: at, Value: point.Value}}}
			values[uid] = point.Value
		}
	}
	keys := make([]string, 0, len(byUID))
	for uid := range byUID {
		keys = append(keys, uid)
	}
	sort.Strings(keys)
	result := make([]metricSeries, 0, len(keys))
	total := 0.0
	for _, uid := range keys {
		result = append(result, byUID[uid])
		total += values[uid]
	}
	count := float64(len(keys))
	summary.Count = &count
	if count > 0 {
		mean := total / count
		summary.Mean = &mean
		summary.Status = "available"
	}
	return result, summary
}

func podSchedulingQuery(metric, namespace string) string {
	return fmt.Sprintf(`%s{namespace=%q}`, metric, namespace)
}
