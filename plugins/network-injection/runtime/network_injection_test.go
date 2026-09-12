package networkinjection

import (
	"context"
	"encoding/json"
	"testing"
)

func TestValidateRejectsJitterAboveLatency(t *testing.T) {
	raw := json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","managedPlatformRef":"art_platform","latencyMilliseconds":50,"jitterMilliseconds":51}`)
	report := (Plugin{}).Validate(context.Background(), Invocation{Input: raw})
	if report.Valid || len(report.Issues) == 0 || report.Issues[0].Path != "jitterMilliseconds" {
		t.Fatalf("unexpected report: %#v", report)
	}
}

func TestNetworkManifestCreatesEveryDirectedZoneLink(t *testing.T) {
	var application applicationDeployment
	application.Spec.Namespace = "experiment-one"
	application.Metadata.OwnershipMarker = "abc123"
	raw, err := networkManifest(application, []string{"zone-a", "zone-b", "zone-c"}, Spec{LatencyMilliseconds: 75, JitterMilliseconds: 5})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Delay map[string]string `json:"delay"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 6 {
		t.Fatalf("expected six directed links, got %d", len(list.Items))
	}
	for _, item := range list.Items {
		if item.Metadata.Labels[profileLabel] != "abc123" || item.Spec.Delay["latency"] != "75ms" || item.Spec.Delay["jitter"] != "5ms" {
			t.Fatalf("unexpected resource: %#v", item)
		}
	}
}
