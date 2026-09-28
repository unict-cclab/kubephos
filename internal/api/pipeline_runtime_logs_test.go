package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStrategyLogTargetFromArtifact(t *testing.T) {
	tests := []struct {
		name     string
		kind     string
		raw      string
		resource string
		arg      string
	}{
		{name: "scheduler", kind: "scheduler", raw: `{"kind":"SchedulerDeployment","spec":{"namespace":"kubephos-run"}}`, resource: "deployment/scheduler", arg: "logs deployment/scheduler -n kubephos-run -c scheduler"},
		{name: "descheduler", kind: "descheduler", raw: `{"kind":"StrategyDeployment","metadata":{"name":"kubephos-run"},"spec":{"strategyKind":"descheduler","namespace":"application"}}`, resource: "deployment/descheduler", arg: "logs deployment/descheduler -n kubephos-run -c descheduler"},
		{name: "autoscaler", kind: "autoscaler", raw: `{"kind":"StrategyDeployment","metadata":{"name":"kubephos-run"},"spec":{"strategyKind":"autoscaler","namespace":"application","targets":["frontend","checkout"]}}`, resource: "kubephos-run-checkout, kubephos-run-frontend", arg: "v1.custompodautoscaler.com/owned-by in (kubephos-run-checkout,kubephos-run-frontend)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := strategyLogTargetFromArtifact(test.kind, test.name, []byte(test.raw))
			if err != nil {
				t.Fatal(err)
			}
			if target.Resource != test.resource || !strings.Contains(strings.Join(target.Args, " "), test.arg) {
				t.Fatalf("unexpected target: %#v", target)
			}
			if target.LokiQuery == "" {
				t.Fatalf("missing Loki fallback query: %#v", target)
			}
		})
	}
}

func TestDecodeLokiRuntimeLogsOrdersAndFiltersEntries(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"status": "success", "data": map[string]any{"result": []any{
		map[string]any{"stream": map[string]string{"pod": "scheduler-1"}, "values": [][]string{{"20", "second"}, {"10", "first"}}},
		map[string]any{"stream": map[string]string{"pod": "scheduler-1"}, "values": [][]string{{"30", "unable to retrieve container logs for containerd://gone"}, {"40", "failed to try resolving symlinks in path /var/log/pods/gone"}}},
	}}})
	lines, err := decodeLokiRuntimeLogs(payload)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "[scheduler-1] first|[scheduler-1] second" {
		t.Fatalf("unexpected Loki lines %#v", lines)
	}
}

func TestStrategyRuntimeResourceNameMatchesKubernetesLimit(t *testing.T) {
	name := strategyRuntimeResourceName("kubephos-run", strings.Repeat("component-", 12))
	if len(name) > 63 || !strings.HasPrefix(name, "kubephos-run-") {
		t.Fatalf("invalid runtime name %q", name)
	}
}
