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

func TestMultiClusterMergesCatalogsAndRunContexts(t *testing.T) {
	rtt := func(cluster string) metricCatalogEntry {
		return metricCatalogEntry{Metric: "network.rtt", Source: sourceMentat, Vantage: vantageNetwork, Collector: collectorPrometheus, Query: `rate(cluster_network_latency_sum{measurement_cluster="` + cluster + `"}[1m])`, RateWindowSeconds: 60, Description: "rtt"}
	}
	cpu := metricCatalogEntry{Metric: "cpu.cores", Source: sourceCadvisor, Vantage: vantageResource, Collector: collectorPrometheus, Query: "cpu", Description: "cpu"}
	merged := mergeCatalogs([][]metricCatalogEntry{{cpu, rtt("a")}, {cpu, rtt("b")}}, []string{"a", "b"})
	if len(merged) != 2 || merged[0] != cpu || merged[1].Query != "a: "+rtt("a").Query+"\nb: "+rtt("b").Query {
		t.Fatalf("unexpected merged catalog %+v", merged)
	}
	one, two := []concurrentLoad{{Namespace: "x", MaxPods: 1}}, []concurrentLoad{{Namespace: "x", MaxPods: 3}, {Namespace: "y", MaxPods: 1}}
	primary := &runContext{Kubernetes: &runContextKubernetes{ServerVersion: "v1.31.2"}, Nodes: []runContextNode{{Name: "n1"}}, ConcurrentLoad: &one}
	peer := &runContext{Kubernetes: &runContextKubernetes{ServerVersion: "v1.30.0"}, Nodes: []runContextNode{{Name: "n1"}}, ConcurrentLoad: &two}
	context := mergeRunContexts([]*runContext{primary, peer, nil}, []string{"a", "b", "c"})
	if context.Kubernetes.ServerVersion != "v1.31.2" || len(context.Nodes) != 2 || context.Nodes[0].Cluster != "a" || context.Nodes[1].Cluster != "b" || len(*context.ConcurrentLoad) != 2 || (*context.ConcurrentLoad)[0].MaxPods != 3 {
		t.Fatalf("unexpected merged run context %+v", context)
	}
	if primary.Nodes[0].Cluster != "" {
		t.Fatal("merging must not mutate the primary context")
	}
}
