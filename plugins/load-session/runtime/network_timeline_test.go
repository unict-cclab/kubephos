package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestNetworkTimelineManifestRestrictsClockAccessAndKeepsLegacyUntouched(t *testing.T) {
	spec, _, deployment, _, profile, _, err := resolve(testStep(t, "start", 1))
	if err != nil {
		t.Fatal(err)
	}
	spec.NetworkTimeline = true
	raw, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, []loadTarget{{URL: "http://frontend"}}, deployment.Spec.Namespace)
	if err != nil {
		t.Fatal(err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	kinds := map[string]map[string]any{}
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		kinds[fmt.Sprint(resource["kind"])] = resource
	}
	role := kinds["Role"]
	rules := role["rules"].([]any)
	rule := rules[0].(map[string]any)
	if fmt.Sprint(rule["verbs"]) != "[get]" || fmt.Sprint(rule["resources"]) != "[configmaps]" || fmt.Sprint(rule["resourceNames"]) != "["+networkClockName(deployment.Metadata.OwnershipMarker)+"]" {
		t.Fatalf("excessive clock permissions: %v", role)
	}
	subject := kinds["RoleBinding"]["subjects"].([]any)[0].(map[string]any)
	if subject["namespace"] != deployment.Spec.Namespace {
		t.Fatalf("clock service account namespace missing: %v", subject)
	}
	pod := kinds["Deployment"]["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	if pod["serviceAccountName"] != networkClockName(deployment.Metadata.OwnershipMarker) {
		t.Fatal("load worker lacks clock identity")
	}
	spec.NetworkTimeline = false
	legacy, err := loadManifest(profile, deployment.Metadata.OwnershipMarker, spec, []loadTarget{{URL: "http://frontend"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacy), "serviceAccountName") || strings.Contains(string(legacy), "kind: Role\n") {
		t.Fatal("legacy load profile gained clock permissions")
	}
}

type clockRunner struct {
	mu            sync.Mutex
	timestamp     string
	wrongDuration bool
	restart       bool
	commands      []string
}

func (r *clockRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	command := strings.Join(args, " ")
	r.commands = append(r.commands, command)
	if strings.Contains(command, "get pods") {
		if strings.Contains(command, "-n chaos-mesh") {
			duration := "60"
			if r.wrongDuration {
				duration = "90"
			}
			restart := 0
			if r.restart {
				restart = 1
			}
			return fmt.Sprintf(`{"items":[{"metadata":{"name":"shaper","labels":{"kubephos.dev/network-duration":%q}},"status":{"phase":"Running","containerStatuses":[{"ready":true,"restartCount":%d}]}}]}`, duration, restart), nil
		}
		return `{"items":[{"metadata":{"name":"load-worker"}}]}`, nil
	}
	if strings.Contains(command, "apply") {
		var value struct {
			Data map[string]string `json:"data"`
		}
		if err := json.Unmarshal(stdin, &value); err != nil {
			return "", err
		}
		r.timestamp = value.Data["startUnix"]
		return "", nil
	}
	if strings.Contains(command, "-c node-shaper") {
		return strconv.FormatInt(time.Now().Unix(), 10), nil
	}
	if strings.Contains(command, "-c locust") {
		return r.timestamp + " " + strconv.FormatInt(time.Now().Unix(), 10), nil
	}
	return "", fmt.Errorf("unexpected clock command %s", command)
}

func TestNetworkClockRejectsWrongDurationAndRestartedShapers(t *testing.T) {
	for _, runner := range []*clockRunner{{wrongDuration: true}, {restart: true}} {
		if _, err := networkShapers(context.Background(), runner, "config", "marker", 60); err == nil {
			t.Fatal("invalid network profile accepted")
		}
	}
}
func TestNetworkAndLoadWorkersAcknowledgeSameStartClock(t *testing.T) {
	runner := &clockRunner{}
	var deployment applicationDeployment
	deployment.Metadata.OwnershipMarker = "marker"
	deployment.Spec.Namespace = "load-namespace"
	profile := loadProfile{Workload: scenarioWorkload("default-load")}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start, err := armNetworkTimeline(ctx, runner, "config", deployment, profile, 60, 1)
	if err != nil {
		t.Fatal(err)
	}
	if runner.timestamp != strconv.FormatInt(start.Unix(), 10) || time.Now().Before(start) {
		t.Fatal("load started before acknowledged network clock")
	}
}
