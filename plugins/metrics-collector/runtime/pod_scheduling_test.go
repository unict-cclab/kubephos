package metricscollector

import (
	"testing"
	"time"
)

func TestPodSchedulingRetainsUIDsOnceAndWeightsPods(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	sample := func(uid string, value float64) metricSeries {
		return metricSeries{Labels: map[string]string{"pod_uid": uid, "workload_pod": "redis-0", "workload": "redis"}, Points: []metricPoint{{Timestamp: start.Add(10 * time.Second), Value: value}, {Timestamp: start.Add(20 * time.Second), Value: value}}}
	}
	latencies := []metricSeries{sample("first", 1000), sample("replacement", 5000), sample("third", 9000), sample("unmatched", 100)}
	scheduled := []metricSeries{sample("first", 1005), sample("replacement", 1008), sample("third", 1025)}
	series, summary := podSchedulingSeries(latencies, scheduled, start, start.Add(time.Minute), 15*time.Second)
	if len(series) != 3 || summary.Count == nil || *summary.Count != 3 || summary.Mean == nil || *summary.Mean != 5000 {
		t.Fatalf("UID deduplication or pod weighting: %+v %+v", series, summary)
	}
	if series[0].Points[0].Timestamp != start.Add(15*time.Second) || series[2].Points[0].Timestamp != start.Add(30*time.Second) {
		t.Fatalf("wrong completion buckets: %+v", series)
	}
	if summary.Scope != "experiment" || summary.TimestampResolutionSeconds == nil || *summary.TimestampResolutionSeconds != 1 {
		t.Fatalf("wrong scope/precision: %+v", summary)
	}
	if err := validateControlSummary(summary); err != nil {
		t.Fatal(err)
	}
}

func TestPodSchedulingKeepsZeroLatencyAndExcludesOutOfRun(t *testing.T) {
	start := time.Unix(1000, 0).UTC()
	item := func(uid string, value float64) metricSeries {
		return metricSeries{Labels: map[string]string{"pod_uid": uid, "workload": "api"}, Points: []metricPoint{{Value: value}}}
	}
	series, summary := podSchedulingSeries([]metricSeries{item("zero", 0), item("before", 200), item("after", 300)}, []metricSeries{item("zero", 1000), item("before", 999), item("after", 1061)}, start, start.Add(time.Minute), 15*time.Second)
	if len(series) != 1 || summary.Mean == nil || *summary.Mean != 0 || summary.Status != "available" {
		t.Fatalf("zero or time filtering: %+v %+v", series, summary)
	}
}
