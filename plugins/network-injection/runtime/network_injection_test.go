package networkinjection

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestValidateRejectsJitterAboveLatency(t *testing.T) {
	raw := json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","managedPlatformRef":"art_platform","nodeGroupLabel":"topology.kubernetes.io/zone","networkInterface":"flannel.1","enableLatency":true,"latencyMilliseconds":50,"jitterMilliseconds":51}`)
	report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw})
	if report.Valid || len(report.Issues) == 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestNetworkManifestCreatesNodeShapersWithEveryImpairment(t *testing.T) {
	var application applicationDeployment
	application.Spec.Namespace = "experiment-one"
	application.Metadata.OwnershipMarker = "abc123"
	latency := 75
	loss := 4.5
	spec := withDefaults(Spec{EnableLatency: true, LatencyMilliseconds: 50, JitterMilliseconds: 5, CorrelationPercent: 10, EnableBandwidth: true, BandwidthBytesPerSecond: 12500000, EnablePacketLoss: true, PacketLossPercent: 1, LinkOverrides: []LinkOverride{{SourceZone: "zone-a", TargetZone: "zone-b", LatencyMilliseconds: &latency, PacketLossPercent: &loss}}})
	nodes := []node{{Name: "node-a", Zone: "zone-a", InternalIP: "10.0.0.1", PodCIDR: "10.42.1.0/24"}, {Name: "node-b", Zone: "zone-b", InternalIP: "10.0.0.2", PodCIDR: "10.42.2.0/24"}, {Name: "node-c", Zone: "zone-c", InternalIP: "10.0.0.3", PodCIDR: "10.42.3.0/24"}}
	raw, err := networkManifest(application, nodes, spec)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Template struct {
					Spec struct {
						Containers []struct {
							Args []string `json:"args"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 3 {
		t.Fatalf("expected three node shapers, got %d", len(list.Items))
	}
	text := list.Items[0].Spec.Template.Spec.Containers[0].Args[0]
	for _, expected := range []string{"delay 75ms 5ms 10%", "rate 12500000bps", "loss 4.5% 10%", "10.42.2.0/24", "10.42.3.0/24"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("shaper script is missing %q:\n%s", expected, text)
		}
	}
	if list.Items[0].Metadata.Labels[profileLabel] != "abc123" {
		t.Fatalf("unexpected ownership labels: %#v", list.Items[0].Metadata.Labels)
	}
}

func TestValidateOverridesRejectsUnknownAndDuplicateLinks(t *testing.T) {
	spec := withDefaults(Spec{EnableLatency: true})
	if err := validateOverrides([]LinkOverride{{SourceZone: "zone-a", TargetZone: "zone-c"}}, []string{"zone-a", "zone-b"}, spec); err == nil {
		t.Fatal("expected unknown zone rejection")
	}
	if err := validateOverrides([]LinkOverride{{SourceZone: "zone-a", TargetZone: "zone-b"}, {SourceZone: "zone-a", TargetZone: "zone-b"}}, []string{"zone-a", "zone-b"}, spec); err == nil {
		t.Fatal("expected duplicate link rejection")
	}
}

func TestNetworkManifestCanShapeOnlyDeclaredPair(t *testing.T) {
	var application applicationDeployment
	application.Metadata.OwnershipMarker = "pair-only"
	latency := 80
	spec := withDefaults(Spec{LinkOverrides: []LinkOverride{{SourceZone: "zone-a", TargetZone: "zone-b", LatencyMilliseconds: &latency}}})
	nodes := []node{{Name: "node-a", Zone: "zone-a", InternalIP: "10.0.0.1", PodCIDR: "10.42.1.0/24"}, {Name: "node-b", Zone: "zone-b", InternalIP: "10.0.0.2", PodCIDR: "10.42.2.0/24"}, {Name: "node-c", Zone: "zone-c", InternalIP: "10.0.0.3", PodCIDR: "10.42.3.0/24"}}
	if err := validateOverrides(spec.LinkOverrides, []string{"zone-a", "zone-b", "zone-c"}, spec); err != nil {
		t.Fatal(err)
	}
	raw, err := networkManifest(application, nodes, spec)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected one source node shaper, got %d", len(list.Items))
	}
	activeNodes, links := profileDimensions(nodes, []string{"zone-a", "zone-b", "zone-c"}, spec)
	if activeNodes != 1 || links != 1 {
		t.Fatalf("unexpected profile dimensions: nodes=%d links=%d", activeNodes, links)
	}
}

func TestVerifyQdiscsChecksEveryKernelParameter(t *testing.T) {
	spec := Spec{EnableLatency: true, LatencyMilliseconds: 100, EnableBandwidth: true, BandwidthBytesPerSecond: 12500000, EnablePacketLoss: true, PacketLossPercent: 20}
	raw := []byte(`[{"kind":"prio","handle":"1:","root":true,"options":{"bands":2}},{"kind":"netem","handle":"20:","parent":"1:2","options":{"delay":{"delay":0.1,"jitter":0,"correlation":0},"loss-random":{"loss":0.2,"correlation":0},"rate":{"rate":12500000}}}]`)
	if err := verifyQdiscs(raw, "node-a", "zone-a", []string{"zone-b"}, spec); err != nil {
		t.Fatal(err)
	}
	var changed []map[string]any
	if err := json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed[1]["options"].(map[string]any)["rate"].(map[string]any)["rate"] = float64(1000)
	mismatch, _ := json.Marshal(changed)
	if err := verifyQdiscs(mismatch, "node-a", "zone-a", []string{"zone-b"}, spec); err == nil {
		t.Fatal("expected bandwidth mismatch")
	}
}
