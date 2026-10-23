package metricscollector

import (
	"strings"
	"testing"
)

func TestMultiClusterCycleMeansUseEventCounts(t *testing.T) {
	a, b, c, d := 2.0, 100.0, 8.0, 300.0
	groups := [][]controlMetricSummary{{{Metric: "scheduler.cycle_duration", Status: "available", Scope: "experiment", Count: &a, Mean: &b}}, {{Metric: "scheduler.cycle_duration", Status: "available", Scope: "experiment", Count: &c, Mean: &d}}}
	result := mergeControlSummaries(groups, []string{"a", "b"})
	if len(result) != 1 || result[0].Count == nil || *result[0].Count != 10 || result[0].Mean == nil || *result[0].Mean != 260 {
		t.Fatalf("incorrect weighted mean: %+v", result)
	}
	groups[1][0].Status = "unavailable"
	result = mergeControlSummaries(groups, []string{"a", "b"})
	if result[0].Status != "unavailable" || result[0].Mean != nil {
		t.Fatal("partial measurements presented as a complete mean")
	}
}
func TestCiliumQueriesUseClusterProbeMetricsAndKeepLocustEndToEnd(t *testing.T) {
	var deployment applicationDeployment
	deployment.Spec.Namespace = "experiment"
	deployment.Spec.Networking = "cilium"
	deployment.Spec.ClusterName = "cluster-a"
	definitions := ciliumMetricDefinitions(deployment, false)
	foundLoad := false
	for _, definition := range definitions {
		if definition.ID == "load.p95_response_time" {
			foundLoad = strings.Contains(definition.Query, "kubephos_load_duration_milliseconds_bucket")
		}
		if strings.HasPrefix(definition.ID, "network.") && (!strings.Contains(definition.Query, "cluster_network_") || !strings.Contains(definition.Query, `measurement_cluster="cluster-a"`)) {
			t.Fatalf("wrong network scope: %s", definition.Query)
		}
		if strings.Contains(definition.Query, "istio_") {
			t.Fatal("Cilium query requires Istio")
		}
	}
	if !foundLoad {
		t.Fatal("missing global Locust latency")
	}
	for _, definition := range ciliumMetricDefinitions(deployment, true) {
		if strings.HasPrefix(definition.ID, "load.") {
			t.Fatal("end-to-end load duplicated across clusters")
		}
	}
}
