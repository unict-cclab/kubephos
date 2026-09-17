package main

import "testing"

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
