package metricscollector

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func controlCounter(values []float64, base time.Time, labels map[string]string) metricSeries {
	s := metricSeries{Labels: labels}
	for i, value := range values {
		s.Points = append(s.Points, metricPoint{Timestamp: base.Add(time.Duration(i) * time.Second), Value: value})
	}
	return s
}

func TestCycleDurationUsesCycleCountsAndHandlesResets(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	sums := []metricSeries{controlCounter([]float64{.2, .5, .05, .15}, base, map[string]string{"pod": "a"})}
	counts := []metricSeries{controlCounter([]float64{2, 3, 1, 2}, base, map[string]string{"pod": "a"})}
	series, total, count := durationCounterSeries("scheduler.cycle_duration", sums, counts, true, "experiment")
	if count != 5 || math.Abs(total-.65) > 1e-9 {
		t.Fatalf("wrong reset-aware totals: %v %v", total, count)
	}
	if len(series.Points) != 4 || math.Abs(series.Points[0].Value-100) > 1e-8 || math.Abs(series.Points[1].Value-300) > 1e-8 {
		t.Fatalf("wrong per-interval duration %+v", series.Points)
	}
	// The run mean is event weighted (130ms), not the mean of the interval means (137.5ms).
	if math.Abs(1000*total/count-130) > 1e-8 {
		t.Fatal("wrong event weighted mean")
	}
	_, clusterTotal, clusterCount := durationCounterSeries("scheduler.cycle_duration", sums, counts, false, "cluster")
	if clusterCount != 3 || math.Abs(clusterTotal-.45) > 1e-8 {
		t.Fatalf("cluster history leaked into the run: %v %v", clusterTotal, clusterCount)
	}
}
func TestIndependentProcessesAndMissingDurationCounters(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	sums := []metricSeries{controlCounter([]float64{.1, .2}, base, map[string]string{"pod": "a"}), controlCounter([]float64{2, 3}, base, map[string]string{"pod": "b"})}
	counts := []metricSeries{controlCounter([]float64{1, 2}, base, map[string]string{"pod": "a"}), controlCounter([]float64{10, 1}, base, map[string]string{"pod": "b"}), controlCounter([]float64{100, 200}, base, map[string]string{"pod": "missing"})}
	_, total, count := durationCounterSeries("descheduler.cycle_duration", sums, counts, true, "experiment")
	if math.Abs(total-5.2) > 1e-8 || count != 13 {
		t.Fatalf("sources were not matched before reset handling: %v %v", total, count)
	}
}
func TestEvictionsAreRunTotalsIncludingObservedResets(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	points, total := cumulativeCounterSeries([]metricSeries{controlCounter([]float64{2, 5, 1, 3}, base, map[string]string{"pod": "a"})}, true, base)
	if total != 8 || len(points) != 4 || points[2].Value != 6 {
		t.Fatalf("wrong cumulative eviction count %v %+v", total, points)
	}
}

type controlUnavailableRunner struct{}

func (controlUnavailableRunner) Run(context.Context, string, ...string) (string, error) {
	return "", errors.New("target unavailable")
}
func TestControlMetricsFailuresDoNotFabricateZero(t *testing.T) {
	deployment := applicationDeployment{}
	deployment.Spec.ControlMetrics = &controlMetricsScope{SchedulerName: "default-scheduler", PodScheduling: true}
	plugin := Plugin{Runner: controlUnavailableRunner{}}
	series, summaries := plugin.collectControlMetrics(context.Background(), clusterConnection{}, deployment, observabilityCapability{}, serviceEndpoint{}, time.Unix(100, 0), time.Unix(110, 0), time.Second)
	if len(series) != 0 || len(summaries) != 4 {
		t.Fatalf("unexpected unavailable metrics: %+v %+v", series, summaries)
	}
	for _, summary := range summaries {
		if summary.Count != nil || summary.Mean != nil || summary.Total != nil {
			t.Fatal("unavailable is not zero")
		}
	}
}
func TestCollectorIncludesDeploymentPhaseAndVerifiesControlSummary(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	plugin := Plugin{Runner: &fakeRunner{now: now}, Clock: func() time.Time { return now }}
	step := testStep(t)
	var deployment applicationDeployment
	if err := json.Unmarshal(step.ResolvedInputs["application-deployment"].Value, &deployment); err != nil {
		t.Fatal(err)
	}
	deployment.Spec.ControlMetrics = &controlMetricsScope{StartedAt: now.Add(-2 * time.Minute), SchedulerName: "default-scheduler"}
	raw, _ := json.Marshal(deployment)
	artifact := step.ResolvedInputs["application-deployment"]
	artifact.Value = raw
	step.ResolvedInputs["application-deployment"] = artifact
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var result result
	if err := json.Unmarshal(value, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Dataset.Spec.Start.Equal(now.Add(-2*time.Minute)) || result.Dataset.Spec.WorkloadStartedAt == nil || !result.Dataset.Spec.WorkloadStartedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("lost deployment/traffic boundaries %+v", result.Dataset.Spec)
	}
	if !strings.Contains(string(value), "scheduler.cycle_duration") {
		t.Fatal("missing native scheduling metrics")
	}
	health, err := plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != "healthy" {
		t.Fatalf("control dataset rejected: %+v %v", health, err)
	}
}

type controlTailRunner struct{ start, end time.Time }

func (r controlTailRunner) Run(_ context.Context, _ string, args ...string) (string, error) {
	parsed, err := url.Parse(strings.TrimPrefix(args[len(args)-1], "--raw="))
	if err != nil {
		return "", err
	}
	query := parsed.Query().Get("query")
	if !strings.Contains(query, "scheduler_scheduling_algorithm_duration_seconds") {
		return `{"status":"success","data":{"resultType":"matrix","result":[]}}`, nil
	}
	start, _ := strconv.ParseInt(parsed.Query().Get("start"), 10, 64)
	end, _ := strconv.ParseInt(parsed.Query().Get("end"), 10, 64)
	isSum := strings.Contains(query, "_sum{")
	value, tail := 2.0, 3.0
	if isSum {
		value, tail = .2, .5
	}
	last := start + ((end-start)/15)*15
	values := [][]any{{start, "0"}, {last, strconv.FormatFloat(value, 'g', -1, 64)}}
	if start == end {
		values = [][]any{{end, strconv.FormatFloat(tail, 'g', -1, 64)}}
	}
	raw, err := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []any{map[string]any{"metric": map[string]string{"instance": "isolated", "pod": "scheduler"}, "values": values}}}})
	return string(raw), err
}
func TestControlMetricsIncludeTheFinalPartialSample(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	end := start.Add(17 * time.Second)
	deployment := applicationDeployment{}
	deployment.Spec.ControlMetrics = &controlMetricsScope{StartedAt: start, SchedulerName: "isolated", Namespace: "control"}
	plugin := Plugin{Runner: controlTailRunner{start: start, end: end}}
	series, summaries := plugin.collectControlMetrics(context.Background(), clusterConnection{}, deployment, observabilityCapability{}, serviceEndpoint{}, start, end, 15*time.Second)
	if len(series) != 1 || len(summaries) == 0 || summaries[0].Count == nil || *summaries[0].Count != 3 || summaries[0].Mean == nil || math.Abs(*summaries[0].Mean-500.0/3) > 1e-8 {
		t.Fatalf("final cycles were lost: %+v %+v", series, summaries)
	}
	if !series[0].Points[len(series[0].Points)-1].Timestamp.Equal(end) {
		t.Fatal("missing exact end of run")
	}
}
