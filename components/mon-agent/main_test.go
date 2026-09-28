package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAnnotateNodePeerMetric(t *testing.T) {
	out := map[string]map[string]string{}
	sample := &model.Sample{
		Metric: model.Metric{
			"origin_node":      "node-a",
			"destination_node": "node-b",
		},
		Value: 12.5,
	}

	annotateNodePeerMetric(out, sample, "network-bandwidth")
	if got, want := out["node-a"]["network-bandwidth.node-b"], "12.500000"; got != want {
		t.Fatalf("bandwidth annotation = %q, want %q", got, want)
	}

}

func TestAnnotatePacketLossConvertsRatioToPercent(t *testing.T) {
	out := map[string]map[string]string{}
	sample := &model.Sample{
		Metric: model.Metric{
			"origin_node":      "node-a",
			"destination_node": "node-b",
		},
		Value: 0.25,
	}

	annotatePacketLoss(out, sample)
	if got, want := out["node-a"]["packet-loss.node-b"], "25.000000"; got != want {
		t.Fatalf("packet-loss annotation = %q, want %q", got, want)
	}
}

func TestAnnotationPatchDeletesLegacyThroughputNames(t *testing.T) {
	patch, err := annotationPatch(map[string]string{"network-throughput": "5"})
	if err != nil {
		t.Fatalf("annotationPatch() error = %v", err)
	}
	var decoded struct {
		Metadata struct {
			Annotations map[string]any `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(patch, &decoded); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	for _, legacy := range []string{"disk-bandwidth", "network-bandwidth"} {
		value, exists := decoded.Metadata.Annotations[legacy]
		if !exists || value != nil {
			t.Errorf("legacy annotation %s is not explicitly deleted", legacy)
		}
	}
}

func TestAnnotateNodePeerMetricRequiresNodeLabels(t *testing.T) {
	out := map[string]map[string]string{}
	annotatePacketLoss(out, &model.Sample{Metric: model.Metric{}, Value: 1})
	if len(out) != 0 {
		t.Fatalf("annotation added without node labels: %#v", out)
	}
}

func TestPacketLossQueryUsesConfiguredRange(t *testing.T) {
	query := packetLossQuery("10m")
	if want := `avg_over_time(node_packet_loss_ratio[10m])`; !strings.Contains(query, want) {
		t.Fatalf("packet loss query = %q, want it to contain %q", query, want)
	}
}

func TestMergeAnnotationsMigratesThroughputNames(t *testing.T) {
	merged := mergeAnnotations(
		map[string]string{
			"disk-bandwidth":         "1",
			"network-bandwidth":      "2",
			"network-bandwidth.peer": "3",
		},
		map[string]string{
			"disk-throughput":    "4",
			"network-throughput": "5",
		},
	)

	if _, exists := merged["disk-bandwidth"]; exists {
		t.Error("legacy disk-bandwidth annotation was retained")
	}
	if _, exists := merged["network-bandwidth"]; exists {
		t.Error("legacy aggregate network-bandwidth annotation was retained")
	}
	if got := merged["network-bandwidth.peer"]; got != "3" {
		t.Fatalf("directional bandwidth annotation = %q, want %q", got, "3")
	}
}

func TestNodeNameFromMetricResolvesExporterAddress(t *testing.T) {
	nodes := []corev1.Node{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "worker-a"},
			Status: corev1.NodeStatus{Addresses: []corev1.NodeAddress{
				{Type: corev1.NodeInternalIP, Address: "192.168.101.63"},
				{Type: corev1.NodeHostName, Address: "worker-a"},
			}},
		},
	}

	metric := model.Metric{"instance": "192.168.101.63:9100"}
	if got := nodeNameFromMetric(metric, nodeNameIndex(nodes)); got != "worker-a" {
		t.Fatalf("node name = %q, want worker-a", got)
	}
}

func TestNodeNameFromMetricRejectsUnknownAddress(t *testing.T) {
	metric := model.Metric{"instance": "192.168.101.99:9100"}
	if got := nodeNameFromMetric(metric, map[string]string{"worker-a": "worker-a"}); got != "" {
		t.Fatalf("node name = %q, want empty", got)
	}
}
