package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
		{name: "descheduler", kind: "descheduler", raw: `{"kind":"StrategyDeployment","metadata":{"name":"kubephos-descheduler-run"},"spec":{"strategyKind":"descheduler","namespace":"kubephos-control-run"}}`, resource: "deployment/descheduler", arg: "logs deployment/descheduler -n kubephos-control-run -c descheduler"},
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

func TestDeschedulerLogsUseNamespaceForLiveAndHistory(t *testing.T) {
	target, err := strategyLogTargetFromArtifact("descheduler", "Descheduler", []byte(`{"kind":"StrategyDeployment","metadata":{"name":"kubephos-descheduler-run"},"spec":{"strategyKind":"descheduler","namespace":"kubephos-control-run"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if target.Namespace != "kubephos-control-run" || target.LokiQuery != `{namespace="kubephos-control-run",container="descheduler"}` {
		t.Fatalf("wrong namespace: %#v", target)
	}
	_, err = strategyLogTargetFromArtifact("descheduler", "Descheduler", []byte(`{"kind":"StrategyDeployment","metadata":{"name":"kubephos-descheduler-run"},"spec":{"strategyKind":"descheduler"}}`))
	if err == nil {
		t.Fatal("missing namespace must not be replaced with a strategy name")
	}
}

func TestCollectStrategyLogsRunsConcurrentlyAndPreservesOrder(t *testing.T) {
	targets := []strategyLogTarget{}
	for _, kind := range []string{"scheduler", "descheduler", "autoscaler"} {
		targets = append(targets, strategyLogTarget{pipelineStrategyLog: pipelineStrategyLog{Kind: kind, Lines: []string{}}, Args: []string{"logs", kind}, LokiQuery: kind})
	}
	started := make(chan string, 3)
	release := make(chan struct{})
	run := func(ctx context.Context, _ string, args ...string) (string, error) {
		started <- args[1]
		select {
		case <-release:
			return args[1] + " entry", nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	done := make(chan []pipelineStrategyLog, 1)
	go func() {
		done <- collectStrategyLogs(context.Background(), targets, "config", nil, time.Time{}, time.Time{}, run, nil)
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for range targets {
		select {
		case <-started:
		case <-timer.C:
			close(release)
			t.Fatal("strategies were not collected concurrently")
		}
	}
	close(release)
	items := <-done
	for i, target := range targets {
		if items[i].Kind != target.Kind || items[i].State != "available" || len(items[i].Lines) != 1 {
			t.Fatalf("unexpected result: %#v", items)
		}
	}
}

func TestCollectStrategyLogsFallsBackWithoutDiscardingOtherTargets(t *testing.T) {
	targets := []strategyLogTarget{
		{pipelineStrategyLog: pipelineStrategyLog{Kind: "scheduler", Lines: []string{}}, Args: []string{"logs", "live"}, LokiQuery: "live"},
		{pipelineStrategyLog: pipelineStrategyLog{Kind: "autoscaler", Lines: []string{}}, Args: []string{"logs", "missing"}, LokiQuery: "history"},
		{pipelineStrategyLog: pipelineStrategyLog{Kind: "descheduler", State: "waiting", Lines: []string{}}},
	}
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[1] == "live" {
			return "live entry", nil
		}
		return "", errors.New("pod removed")
	}
	history := func(_ context.Context, _, query string, _, _ time.Time) ([]string, error) {
		if query != "history" {
			t.Errorf("unexpected history query %q", query)
		}
		return []string{"archived entry"}, nil
	}
	items := collectStrategyLogs(context.Background(), targets, "config", nil, time.Time{}, time.Time{}, run, history)
	if items[0].Lines[0] != "live entry" || items[1].Lines[0] != "archived entry" || items[2].State != "waiting" {
		t.Fatalf("unexpected results: %#v", items)
	}
}
