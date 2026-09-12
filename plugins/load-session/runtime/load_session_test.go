package loadsession

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
)

func TestLoadSessionStartStopResume(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	plugin := Plugin{Runner: runner}
	start := testStep(t, "start", 2)
	health, err := plugin.Precheck(context.Background(), start, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("start precheck failed %#v %v", health, err)
	}
	started, err := plugin.Execute(context.Background(), start, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), start, started, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || runner.state.Spec.Replicas != 2 {
		t.Fatalf("start verify failed %#v %v", health, err)
	}

	stop := testStep(t, "stop", 1)
	health, err = plugin.Precheck(context.Background(), stop, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("stop precheck failed %#v %v", health, err)
	}
	stopped, err := plugin.Execute(context.Background(), stop, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), stop, stopped, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || runner.state.Spec.Replicas != 0 {
		t.Fatalf("stop verify failed %#v %v", health, err)
	}

	resume := testStep(t, "resume", 3)
	health, err = plugin.Precheck(context.Background(), resume, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("resume precheck failed %#v %v", health, err)
	}
	resumed, err := plugin.Execute(context.Background(), resume, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), resume, resumed, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || runner.state.Spec.Replicas != 3 {
		t.Fatalf("resume verify failed %#v %v", health, err)
	}
}

func TestLoadSessionRejectsInvalidTransition(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker", exists: true}
	runner.state.Metadata.Annotations = map[string]string{applicationOwnershipKey: "application-marker", loadProfileKey: "default-load"}
	runner.state.Spec.Replicas = 1
	health, err := (Plugin{Runner: runner}).Precheck(context.Background(), testStep(t, "resume", 1), discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected invalid transition %#v %v", health, err)
	}
}

func TestLoadManifestBuildsGenericLocustRuntime(t *testing.T) {
	profile := loadProfile{ID: "journey", Engine: "locust", RuntimeImage: "example.test/locust:1.0.0", Script: "from locust import HttpUser\n", Workload: scenarioWorkload("journey")}
	manifest, err := loadManifest(profile, "owner", Spec{Users: 25, SpawnRate: 5, Pattern: "constant"}, []loadTarget{{URL: "http://node-proxy:80/", Weight: 1}})
	if err != nil {
		t.Fatal(err)
	}
	value := string(manifest)
	for _, expected := range []string{"kind: ConfigMap", "kind: Deployment", "from locust import HttpUser", "http://node-proxy:80/", "kubephos.dev/role: management", "example.test/locust:1.0.0"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("generated runtime is missing %q:\n%s", expected, value)
		}
	}
}

func TestResolveLoadTargetsUsesNodeProxyAndZoneWeights(t *testing.T) {
	runner := geographicRunner{}
	targets, err := resolveLoadTargets(context.Background(), runner, "config", "experiment", serviceEndpoint{Service: "node-proxy", Port: 80, Protocol: "http", Path: "/"}, Spec{ZoneWeights: map[string]int{"zone-a": 3, "zone-b": 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 3 || targets[0].Zone != "zone-a" || targets[0].Weight != 3 || targets[2].Zone != "zone-b" || targets[2].Weight != 1 {
		t.Fatalf("unexpected geographic targets: %#v", targets)
	}
	for _, target := range targets {
		if !strings.Contains(target.URL, ":31234/") {
			t.Fatalf("target does not use the node proxy: %#v", target)
		}
	}
}

func TestLoadSessionCleanupRestoresState(t *testing.T) {
	runner := &fakeRunner{namespaceMarker: "application-marker"}
	plugin := Plugin{Runner: runner}
	start := testStep(t, "start", 2)
	if _, err := plugin.Execute(context.Background(), start, discardLog); err != nil {
		t.Fatal(err)
	}
	start.Cleanup = true
	if health, err := plugin.Precheck(context.Background(), start, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("start cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), start, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), start, nil, discardLog); err != nil || health.Status != domain.HealthHealthy || runner.exists {
		t.Fatalf("start cleanup verify failed %#v %v", health, err)
	}

	if _, err := plugin.Execute(context.Background(), testStep(t, "start", 2), discardLog); err != nil {
		t.Fatal(err)
	}
	stop := testStep(t, "stop", 1)
	if _, err := plugin.Execute(context.Background(), stop, discardLog); err != nil {
		t.Fatal(err)
	}
	stop.Cleanup = true
	if health, err := plugin.Precheck(context.Background(), stop, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("stop cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), stop, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), stop, nil, discardLog); err != nil || health.Status != domain.HealthHealthy || runner.state.Spec.Replicas != 2 {
		t.Fatalf("stop cleanup verify failed %#v %v", health, err)
	}
}

func testStep(t *testing.T, action string, replicas int) domain.PlanStep {
	t.Helper()
	spec := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", LoadScenarioSetRef: "art_scenarios", ScenarioID: "default-load", Action: action, Replicas: replicas, Users: 20, SpawnRate: 2}
	raw, _ := json.Marshal(spec)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	return step
}

func testArtifacts(t *testing.T) map[string]domain.ResolvedArtifact {
	t.Helper()
	certificate := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + certificate + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	deployment, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ApplicationDeployment", "metadata": map[string]string{"name": "kubephos-app-one", "version": "v1alpha1", "ownershipMarker": "application-marker"}, "spec": map[string]any{"applicationRef": "app:dev.example.app@1.0.0", "clusterServer": "https://10.10.0.10:6443", "namespace": "kubephos-app-one", "manifestDigest": "sha256:" + strings.Repeat("a", 64), "endpoints": []map[string]any{{"id": "frontend-http", "component": "frontend", "service": "frontend", "port": 80, "protocol": "http"}}}})
	script := "from locust import HttpUser\nclass User(HttpUser):\n    pass\n"
	scriptHash := sha256.Sum256([]byte(script))
	scenarios := []loadProfile{{ID: "default-load", Engine: "locust", TargetEndpoint: "frontend-http", RuntimeImage: "example.invalid/load:1.0.0", Script: script, ScriptDigest: "sha256:" + hex.EncodeToString(scriptHash[:])}}
	digest, err := digestJSON(scenarios)
	if err != nil {
		t.Fatal(err)
	}
	profileSet, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "LoadScenarioSet", "metadata": map[string]string{"applicationRef": "app:dev.example.app@1.0.0", "digest": digest}, "spec": map[string]any{"scenarios": scenarios}})
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "application-deployment": {Value: deployment}, "load-scenario-set": {Value: profileSet}}
}

func discardLog(string, string) error { return nil }

type geographicRunner struct{}

func (geographicRunner) Run(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if strings.HasPrefix(command, "get service node-proxy ") {
		return `{"spec":{"ports":[{"port":80,"nodePort":31234}]}}`, nil
	}
	if strings.HasPrefix(command, "get nodes ") {
		return `{"items":[{"metadata":{"labels":{"topology.kubernetes.io/zone":"zone-a"}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.11"}]}},{"metadata":{"labels":{"topology.kubernetes.io/zone":"zone-a"}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.12"}]}},{"metadata":{"labels":{"topology.kubernetes.io/zone":"zone-b"}},"status":{"addresses":[{"type":"InternalIP","address":"10.0.0.21"}]}}]}`, nil
	}
	return "", errors.New("unexpected command: " + command)
}

type fakeRunner struct {
	namespaceMarker string
	exists          bool
	state           workloadState
}

func (runner *fakeRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "auth can-i") {
		return "yes\n", nil
	}
	if strings.HasPrefix(command, "get namespace ") {
		return runner.namespaceMarker, nil
	}
	if strings.HasPrefix(command, "get deployment/kubephos-load-default-load ") {
		if !runner.exists {
			return "", nil
		}
		value, _ := json.Marshal(runner.state)
		return string(value), nil
	}
	if strings.HasPrefix(command, "apply --dry-run=server") {
		return "accepted", nil
	}
	if strings.HasPrefix(command, "get endpoints frontend ") {
		return "10.42.0.12", nil
	}
	if strings.HasPrefix(command, "apply --namespace ") {
		var resource map[string]any
		if err := yaml.Unmarshal(stdin, &resource); err != nil {
			return "", err
		}
		metadata, _ := resource["metadata"].(map[string]any)
		annotations, _ := metadata["annotations"].(map[string]any)
		runner.exists = true
		runner.state.Metadata.Annotations = map[string]string{applicationOwnershipKey: annotations[applicationOwnershipKey].(string), loadProfileKey: annotations[loadProfileKey].(string)}
		runner.state.Spec.Replicas = 1
		return "created", nil
	}
	if strings.HasPrefix(command, "scale deployment/kubephos-load-default-load ") {
		value, err := strconv.Atoi(args[len(args)-1])
		if err != nil {
			return "", err
		}
		runner.state.Spec.Replicas = value
		runner.state.Status.ReadyReplicas = value
		return "scaled", nil
	}
	if strings.HasPrefix(command, "annotate deployment/kubephos-load-default-load ") {
		parts := strings.Split(args[4], "=")
		if len(parts) != 2 {
			return "", errors.New("invalid annotation")
		}
		if runner.state.Metadata.Annotations == nil {
			runner.state.Metadata.Annotations = map[string]string{}
		}
		runner.state.Metadata.Annotations[parts[0]] = parts[1]
		return "annotated", nil
	}
	if strings.HasPrefix(command, "rollout status deployment/kubephos-load-default-load ") {
		runner.state.Status.ReadyReplicas = runner.state.Spec.Replicas
		return "ready", nil
	}
	if strings.HasPrefix(command, "get pods ") {
		if runner.state.Spec.Replicas == 0 {
			return "", nil
		}
		return "pod/kubephos-load-default-load-one", nil
	}
	if strings.HasPrefix(command, "delete deployment/kubephos-load-default-load ") {
		runner.exists = false
		return "deleted", nil
	}
	return "", errors.New("unexpected command: " + command)
}
