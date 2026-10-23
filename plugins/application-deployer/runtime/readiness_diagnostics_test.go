package applicationdeployer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

const unschedulableGateway = `{"items":[{"metadata":{"name":"s0-ready"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"gateway-cloud"},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"0/9 nodes are available: node affinity mismatch"}]}}]}`

func TestReadinessSummaryPreservesSchedulingReasonAndOmitsReadyPods(t *testing.T) {
	summary, count, err := workloadReadinessSummary(unschedulableGateway)
	if err != nil || count != 1 {
		t.Fatalf("unexpected result %q %d %v", summary, count, err)
	}
	for _, value := range []string{"1/2 pods ready", "gateway-cloud [Pending]", "PodScheduled: Unschedulable", "node affinity mismatch"} {
		if !strings.Contains(summary, value) {
			t.Fatalf("missing %q in %s", value, summary)
		}
	}
	if strings.Contains(summary, "s0-ready") {
		t.Fatal("healthy replicas should not fill the log")
	}
}

func TestReadinessSummaryIncludesImageAndInitContainerFailures(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"frontend"},"status":{"phase":"Pending","initContainerStatuses":[{"name":"init","state":{"waiting":{"reason":"CrashLoopBackOff","message":"init failed"}}}],"containerStatuses":[{"name":"app","state":{"waiting":{"reason":"ImagePullBackOff","message":"registry certificate is invalid"}}}]}}]}`
	summary, _, err := workloadReadinessSummary(raw)
	if err != nil || !strings.Contains(summary, "init: CrashLoopBackOff") || !strings.Contains(summary, "app: ImagePullBackOff") {
		t.Fatalf("missing container diagnostics: %s %v", summary, err)
	}
}

func TestReadinessSummaryBoundsPodCountAndMessages(t *testing.T) {
	items := []any{}
	for i := 0; i < 50; i++ {
		items = append(items, map[string]any{"metadata": map[string]any{"name": "pending-pod"}, "status": map[string]any{"phase": "Pending", "conditions": []any{map[string]any{"type": "PodScheduled", "status": "False", "reason": "Unschedulable", "message": strings.Repeat("x", 2000)}}}})
	}
	raw, _ := json.Marshal(map[string]any{"items": items})
	summary, count, err := workloadReadinessSummary(string(raw))
	if err != nil || count != 50 || strings.Count(summary, "pending-pod") != 8 || !strings.Contains(summary, "42 additional pods") || len(summary) > 3500 {
		t.Fatalf("diagnostics are not bounded: count=%d size=%d err=%v", count, len(summary), err)
	}
}

func TestReadinessFailureCapturesReasonAfterDeadlineWithoutLosingCause(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	runner := readinessRunner(func(ctx context.Context, _ string, _ []byte, args ...string) (string, error) {
		if ctx.Err() != nil {
			t.Fatal("diagnostic query reused the expired readiness context")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("diagnostic query is not bounded")
		}
		if strings.Join(args, " ") != "get pods -n namespace -o json" {
			t.Fatalf("unexpected diagnostic command: %v", args)
		}
		return unschedulableGateway, nil
	})
	err := workloadReadinessFailure(ctx, runner, "config", "namespace", context.DeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "node affinity mismatch") {
		t.Fatalf("cause or diagnosis was lost: %v", err)
	}
}

func TestReadinessFailureDoesNotStartQueriesAfterExplicitCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		t.Fatal("query started after cancellation")
		return "", nil
	})
	if err := workloadReadinessFailure(ctx, runner, "config", "namespace", context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestReadinessFailureKeepsOriginalErrorIfDiagnosticsAreUnavailable(t *testing.T) {
	cause := errors.New("readiness timed out")
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		return "", errors.New("API unavailable")
	})
	if err := workloadReadinessFailure(context.Background(), runner, "config", "namespace", cause); err != cause {
		t.Fatalf("original cause was replaced: %v", err)
	}
}

func TestWorkloadVerificationDeadlineRetainsPendingPodReason(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	runner := readinessRunner(func(ctx context.Context, _ string, _ []byte, args ...string) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if strings.Join(args, " ") == "get pods -n namespace -o json" {
			return unschedulableGateway, nil
		}
		return "", errors.New("unexpected diagnostic command")
	})
	err := verifyWorkloads(ctx, runner, "config", "namespace", []workloadTarget{{ID: "gateway-cloud", Kind: "Deployment", Name: "gateway-cloud"}}, discardLog)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "gateway-cloud") || !strings.Contains(err.Error(), "node affinity mismatch") {
		t.Fatalf("verification lost the failure details: %v", err)
	}
}
