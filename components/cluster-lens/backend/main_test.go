package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadGatewayTraffic(t *testing.T) {
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"destination_workload":"gateway-node-a","destination_workload_namespace":"run"},"value":[1,"4.5"]}]}}`))
	}))
	defer prometheus.Close()
	s := &server{prometheusURL: prometheus.URL, httpClient: prometheus.Client()}
	values, err := s.readGatewayTraffic(context.Background(), map[string]gatewayTarget{
		key("run", "gateway-node-a"): {Namespace: "run", Workload: "gateway-node-a", Pod: "gateway-node-a-1", Node: "node-a", Zone: "zone-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].Zone != "zone-a" || values[0].RequestsPerSecond != 4.5 || values[0].Requests != 270 {
		t.Fatalf("unexpected gateway traffic: %#v", values)
	}
}

func TestReadApplicationTrafficUsesSuccessfulEntrypointRequests(t *testing.T) {
	queries := []string{}
	prometheus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		queries = append(queries, query)
		w.Header().Set("Content-Type", "application/json")
		value := "8.75"
		if strings.Contains(query, "histogram_quantile") {
			value = "42.5"
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"result":[{"metric":{"source_workload_namespace":"run"},"value":[1,"` + value + `"]}]}}`))
	}))
	defer prometheus.Close()
	s := &server{prometheusURL: prometheus.URL, httpClient: prometheus.Client()}
	values, err := s.readApplicationTraffic(context.Background(), map[string]gatewayTarget{
		key("run", "gateway-node-a"): {Namespace: "run", Workload: "gateway-node-a"},
		key("run", "gateway-node-b"): {Namespace: "run", Workload: "gateway-node-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].ThroughputRequestsPerSecond != 8.75 || values[0].P95ResponseTimeMilliseconds != 42.5 {
		t.Fatalf("unexpected application traffic: %#v", values)
	}
	if len(queries) != 2 || !strings.Contains(queries[0], `response_code=~"2..|3.."`) || !strings.Contains(queries[0], `source_workload=~"^(gateway-node-a|gateway-node-b)$"`) {
		t.Fatalf("throughput query does not select successful entrypoint traffic: %q", queries)
	}
}

func TestSelectedNodeAnnotationsMigratesThroughputNames(t *testing.T) {
	annotations := selectedNodeAnnotations(map[string]string{
		"disk-bandwidth":               "10",
		"network-bandwidth":            "20",
		"network-bandwidth.node-b":     "30",
		"packet-loss.node-b":           "10",
		"network-latency.node-b":       "5",
		"unrelated.example/annotation": "ignored",
	})

	for name, want := range map[string]string{
		"disk-throughput":          "10",
		"network-throughput":       "20",
		"network-bandwidth.node-b": "30",
		"packet-loss.node-b":       "10",
		"network-latency.node-b":   "5",
	} {
		if got := annotations[name]; got != want {
			t.Errorf("annotation %s = %q, want %q", name, got, want)
		}
	}
	if _, exists := annotations["disk-bandwidth"]; exists {
		t.Error("legacy annotation was not canonicalized")
	}
}

func TestAppendNodeEdgesCombinesMentatMetrics(t *testing.T) {
	snap := snapshot{}
	appendNodeEdges(&snap, "node-a", map[string]string{
		"network-latency.node-b":   "5.5",
		"network-bandwidth.node-b": "1048576",
		"packet-loss.node-b":       "25",
		"packet-loss.node-a":       "0",
	})

	if len(snap.NodeEdges) != 1 {
		t.Fatalf("node edges = %d, want 1", len(snap.NodeEdges))
	}
	edge := snap.NodeEdges[0]
	if edge.Source != "node-a" || edge.Target != "node-b" || edge.Value != 5.5 {
		t.Fatalf("unexpected edge: %#v", edge)
	}
	if edge.Bandwidth == nil || *edge.Bandwidth != 1048576 {
		t.Fatalf("bandwidth = %v, want 1048576", edge.Bandwidth)
	}
	if edge.PacketLoss == nil || *edge.PacketLoss != 25 {
		t.Fatalf("packet loss = %v, want 25", edge.PacketLoss)
	}
}

func TestParseNodeMetrics(t *testing.T) {
	metrics, err := parseNodeMetrics([]byte(`{"items":[{"metadata":{"name":"node-a"},"usage":{"cpu":"125m","memory":"256Mi"}},{"metadata":{"name":"invalid"},"usage":{"cpu":"bad","memory":"1Gi"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(metrics) != 1 || metrics["node-a"].CPU != 125 || metrics["node-a"].Memory != 268435456 {
		t.Fatalf("unexpected metrics: %#v", metrics)
	}
}

func TestNodeRoleUsesManagedRoleLabel(t *testing.T) {
	if got := nodeRole(map[string]string{"kubephos.dev/role": "application"}); got != "application" {
		t.Fatalf("role = %q", got)
	}
	if got := nodeRole(map[string]string{"node-role.kubernetes.io/control-plane": "true", "kubephos.dev/role": "control-plane"}); got != "control-plane" {
		t.Fatalf("control-plane role = %q", got)
	}
}
