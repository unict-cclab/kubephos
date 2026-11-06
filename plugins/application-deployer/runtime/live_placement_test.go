package applicationdeployer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/placement"
)

func TestLivePlacementRemovesOldCustomValuesAndPreservesDefaults(t *testing.T) {
	previous := &placement.ApplicationSettings{NodeSelector: map[string]string{"kubephos.dev/pool": "wrong"}, Tolerations: []placement.Toleration{{Key: "old", Effect: "NoSchedule"}}}
	annotation, _ := json.Marshal(previous)
	object := map[string]any{"metadata": map[string]any{"resourceVersion": "7", "annotations": map[string]any{placementAnnotation: string(annotation), "keep": "yes"}}, "kind": "Deployment", "spec": map[string]any{"template": map[string]any{"spec": map[string]any{"nodeSelector": map[string]any{"kubephos.dev/role": "application", "kubephos.dev/pool": "wrong", "other": "keep"}, "tolerations": []any{map[string]any{"key": "old", "effect": "NoSchedule"}, map[string]any{"key": "managed", "effect": "NoSchedule"}}}}}}
	baseline := placement.WorkloadDefaults{NodeSelector: map[string]string{"kubephos.dev/role": "application"}, Tolerations: []placement.Toleration{{Key: "managed", Effect: "NoSchedule"}}}
	next := &placement.ApplicationSettings{NodeSelector: map[string]string{"kubephos.dev/pool": "edge"}}
	patch, err := livePlacementPatch(object, "api", baseline, PlacementControl{Settings: next})
	if err != nil {
		t.Fatal(err)
	}
	if patch[0]["op"] != "test" || patch[0]["value"] != "7" {
		t.Fatal("missing concurrent update guard", patch)
	}
	selector := patch[2]["value"].(map[string]string)
	if selector["kubephos.dev/pool"] != "edge" || selector["kubephos.dev/role"] != "application" || selector["other"] != "keep" {
		t.Fatal(selector)
	}
	tolerations := patch[3]["value"].([]any)
	if len(tolerations) != 1 {
		t.Fatal("removed defaults or retained old custom toleration", tolerations)
	}
	patch, err = livePlacementPatch(object, "api", baseline, PlacementControl{})
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := patch[2]["value"].(map[string]string)["kubephos.dev/pool"]; exists {
		t.Fatal("old selector retained when custom settings cleared")
	}
	next.NodeSelector["kubephos.dev/role"] = "management"
	if _, err = livePlacementPatch(object, "api", baseline, PlacementControl{Settings: next}); err == nil {
		t.Fatal("changed managed selector")
	}
}
func TestProxyDefaultsComeFromInjectorValues(t *testing.T) {
	raw := []byte(`{"data":{"values":"{\"global\":{\"proxy\":{\"resources\":{\"requests\":{\"cpu\":\"125m\",\"memory\":\"96Mi\"},\"limits\":{\"memory\":\"768Mi\"}}}}}"}}`)
	values, err := parseProxyDefaults(raw)
	if err != nil {
		t.Fatal(err)
	}
	if values["cpuRequest"] != "125m" || values["memoryLimit"] != "768Mi" || values["cpuLimit"] != "" {
		t.Fatal(values)
	}
	if _, err = parseProxyDefaults([]byte(`{"data":{}}`)); err == nil {
		t.Fatal("invented defaults without injector values")
	}
}
func TestLivePlacementControlIsOptIn(t *testing.T) {
	for _, name := range []string{"other", "kubephos-placement-../x", "kubephos-placement-deployment_x"} {
		if validPlacementControlName(name) {
			t.Fatal(name)
		}
	}
	if !validPlacementControlName("kubephos-placement-deployment-123") {
		t.Fatal("rejected valid control")
	}
}

func TestPendingJobReplacementRetainsApplicationAndDropsServerIdentity(t *testing.T) {
	obj := map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": "bootstrap", "uid": "old", "resourceVersion": "9"}, "status": map[string]any{"active": float64(1)}, "spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"controller-uid": "old"}}, "template": map[string]any{"metadata": map[string]any{"labels": map[string]any{"controller-uid": "old", "app": "bootstrap"}}, "spec": map[string]any{"containers": []any{map[string]any{"name": "bootstrap", "image": "example:test"}}, "nodeSelector": map[string]any{"pool": "wrong"}}}}}
	patch, err := livePlacementPatch(obj, "Job/bootstrap", placement.WorkloadDefaults{}, PlacementControl{Settings: &placement.ApplicationSettings{NodeSelector: map[string]string{"pool": "edge"}}})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := pendingJobReplacement(obj, patch, "application")
	if err != nil {
		t.Fatal(err)
	}
	meta := replacement["metadata"].(map[string]any)
	if meta["uid"] != nil || meta["resourceVersion"] != nil || meta["namespace"] != "application" || replacement["status"] != nil {
		t.Fatal("reused server identity", replacement)
	}
	spec := replacement["spec"].(map[string]any)
	if spec["selector"] != nil {
		t.Fatal("retained generated selector")
	}
	pod := applicationPodSpec(replacement)
	if pod["nodeSelector"].(map[string]string)["pool"] != "edge" || len(pod["containers"].([]any)) != 1 {
		t.Fatal("lost application or placement", pod)
	}
	labels := spec["template"].(map[string]any)["metadata"].(map[string]any)["labels"].(map[string]any)
	if labels["controller-uid"] != nil || labels["app"] != "bootstrap" {
		t.Fatal(labels)
	}
}

func TestPendingRecoveryDoesNotRestartStartedPods(t *testing.T) {
	pending := map[string]any{"status": map[string]any{"phase": "Pending"}}
	if !podNeverStarted(pending) {
		t.Fatal("unscheduled pod rejected")
	}
	pending["status"].(map[string]any)["initContainerStatuses"] = []any{map[string]any{"state": map[string]any{"running": map[string]any{"startedAt": "now"}}}}
	if podNeverStarted(pending) {
		t.Fatal("restarted an executing initialization")
	}
	pending["status"] = map[string]any{"phase": "Succeeded"}
	if podNeverStarted(pending) {
		t.Fatal("restarted completed work")
	}
}

type guardedDeleteRunner struct {
	options map[string]any
	args    []string
}

func (r *guardedDeleteRunner) Run(_ context.Context, _ string, in []byte, args ...string) (string, error) {
	r.args = args
	_ = json.Unmarshal(in, &r.options)
	return "", nil
}
func TestPendingRecoveryUsesUIDAndVersionPreconditions(t *testing.T) {
	runner := &guardedDeleteRunner{}
	if err := deleteWithPreconditions(context.Background(), runner, "config", "/api/v1/namespaces/app/pods/pod", map[string]any{"uid": "pod-uid", "resourceVersion": "11"}); err != nil {
		t.Fatal(err)
	}
	guard := runner.options["preconditions"].(map[string]any)
	if guard["uid"] != "pod-uid" || guard["resourceVersion"] != "11" || runner.options["gracePeriodSeconds"] != nil {
		t.Fatal("unsafe deletion", runner.options)
	}
	if err := deleteWithPreconditions(context.Background(), runner, "config", "/path", map[string]any{}); err == nil {
		t.Fatal("deleted without verified identity")
	}
}

func TestBootstrapReadinessFollowsPendingJobReplacement(t *testing.T) {
	calls := 0
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("Error from server (NotFound): jobs.batch bootstrap not found")
		}
		return "completed", nil
	})
	if err := waitBootstrapCompletion(context.Background(), runner, "config", "app", "bootstrap"); err != nil || calls != 2 {
		t.Fatal("replacement ended provisioning", calls, err)
	}
	cause := errors.New("authorization denied")
	runner = readinessRunner(func(context.Context, string, []byte, ...string) (string, error) { return "", cause })
	if err := waitBootstrapCompletion(context.Background(), runner, "config", "app", "bootstrap"); !errors.Is(err, cause) {
		t.Fatal("masked an actual failure", err)
	}
}

func TestPlacementFollowerStartsImmediatelyAndStopsWithProvisioning(t *testing.T) {
	started := make(chan struct{}, 1)
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		return "", nil
	})
	stop := followPlacement(context.Background(), runner, "config", stepInput{Spec: Spec{PlacementControlName: "kubephos-placement-run-test"}}, "", nil, discardLog)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("placement was not checked before readiness polling")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("placement follower outlived provisioning")
	}
}
