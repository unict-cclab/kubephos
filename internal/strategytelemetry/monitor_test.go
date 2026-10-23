package strategytelemetry

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMonitorUsesScopedDiscoveryAndReadOnlyMetricsIdentity(t *testing.T) {
	resources := Resources("experiment-control", "scheduler", "isolated", "ownership", "marker", map[string]string{"app": "scheduler"}, "healthz")
	raw, _ := json.Marshal(resources)
	text := string(raw)
	for _, expected := range []string{"system:monitoring", "PodMonitor", "scheduler-metrics", "credentials", "kubephos-observability", "experiment-control", "healthz"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	if strings.Contains(text, "cluster-admin") {
		t.Fatal("scrape identity must not grant cluster administration")
	}
}
