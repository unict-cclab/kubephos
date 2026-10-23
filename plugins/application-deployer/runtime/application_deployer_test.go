package applicationdeployer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestApplicationDeploymentLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner, StabilityChecks: 1}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 4 || len(plan.Steps[0].Outputs) != 1 || plan.Steps[0].Outputs[0].Type != "ApplicationDeployment" {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ApplicationDeployment.Spec.ControlMetrics == nil || !decoded.ApplicationDeployment.Spec.ControlMetrics.PodScheduling {
		t.Fatal("pod observer was not declared in deployment evidence")
	}
	if !strings.HasPrefix(decoded.ApplicationDeployment.Spec.Namespace, "kubephos-app-") || len(decoded.ApplicationDeployment.Spec.Workloads) != 1 || len(decoded.ApplicationDeployment.Spec.Endpoints) != 1 {
		t.Fatalf("unexpected result %#v", decoded)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed %#v %v", health, err)
	}
}

func TestPreparedApplicationCreatesAndCleansSharedControlNamespace(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner, StabilityChecks: 1}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints","mode":"prepare","controlNamespace":true}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(decoded.ApplicationDeployment.Spec.ControlNamespace, "kubephos-control-") || !runner.installed || !runner.controlInstalled {
		t.Fatalf("shared control namespace was not prepared: result=%#v runner=%#v", decoded, runner)
	}
	health, err = plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}

	// The deploy-stage cleanup runs earlier in reverse pipeline order and may
	// already have removed the application namespace.
	runner.installed = false
	step.Cleanup = true
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatal(err)
	}
	if runner.controlInstalled {
		t.Fatal("shared control namespace remains after prepare-stage cleanup")
	}
	health, err = plugin.Verify(context.Background(), step, nil, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed %#v %v", health, err)
	}
}

func TestValidationRejectsMissingAndDuplicateReferences(t *testing.T) {
	values := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"clusterConnectionRef":"art_same","manifestSetRef":"art_same","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`),
	}
	for _, value := range values {
		if (Plugin{}).Validate(context.Background(), Invocation{Input: value}).Valid {
			t.Fatalf("expected validation rejection for %s", value)
		}
	}
}

func TestManifestIsolationRejectsNamespaceAndClusterScope(t *testing.T) {
	clusterScoped := map[string]bool{"Namespace": true, "ClusterRole": true}
	values := []string{
		"apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: value\n  namespace: default\n",
		"apiVersion: v1\nkind: Namespace\nmetadata:\n  name: value\n",
		"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: value\nrules: []\n",
		"apiVersion: v1\nkind: List\nmetadata:\n  name: value\nitems: []\n",
	}
	for _, value := range values {
		if err := validateManifestIsolation([]byte(value), clusterScoped); err == nil {
			t.Fatalf("expected isolation rejection for %s", value)
		}
	}
}

func TestPodDisruptionBudgetsProtectEachDeschedulableWorkload(t *testing.T) {
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: checkout\n"
	workloads := []workloadTarget{
		{ID: "checkout", APIVersion: "apps/v1", Kind: "Deployment", Name: "checkout", Selector: map[string]string{"app": "checkout", "version": "v1"}, Traits: []string{"deschedulable"}},
		{ID: "gateway", APIVersion: "apps/v1", Kind: "Deployment", Name: "gateway", Selector: map[string]string{"app": "gateway"}, Traits: []string{"traffic-entrypoint"}},
	}

	result, err := addPodDisruptionBudgets(manifest, workloads, 2)
	if err != nil {
		t.Fatal(err)
	}
	resources, err := decodeManifestResources(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 2 {
		t.Fatalf("resources = %d, want deployment and one PDB", len(resources))
	}
	pdb := resources[1]
	if pdb["apiVersion"] != "policy/v1" || pdb["kind"] != "PodDisruptionBudget" {
		t.Fatalf("unexpected PDB identity %#v", pdb)
	}
	metadata := pdb["metadata"].(map[string]any)
	spec := pdb["spec"].(map[string]any)
	selector := spec["selector"].(map[string]any)["matchLabels"].(map[string]any)
	if metadata["name"] != "kubephos-checkout" || spec["minAvailable"] != 2 || selector["app"] != "checkout" || selector["version"] != "v1" {
		t.Fatalf("unexpected PDB %#v", pdb)
	}
	withoutPDB, err := addPodDisruptionBudgets(manifest, workloads, 0)
	if err != nil || withoutPDB != manifest {
		t.Fatalf("disabled PDB generation changed the manifest: %v", err)
	}
}

func TestPrecheckRejectsManifestDigestMismatch(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{}}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	var manifests manifestSet
	if err := json.Unmarshal(step.ResolvedInputs["manifest-set"].Value, &manifests); err != nil {
		t.Fatal(err)
	}
	manifests.Metadata.Digest = "sha256:" + strings.Repeat("0", 64)
	value, _ := json.Marshal(manifests)
	step.ResolvedInputs["manifest-set"] = domain.ResolvedArtifact{Value: value}
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected digest rejection %#v %v", health, err)
	}
}

func TestVerificationRejectsReadinessLostDuringStabilityWindow(t *testing.T) {
	runner := &fakeRunner{failReadyAt: 2}
	plugin := Plugin{Runner: runner, StabilityChecks: 2, StabilityInterval: time.Millisecond}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected unstable workload rejection, got %#v %v", health, err)
	}
}

func TestTrafficEntrypointIsRecreatedAsOneDeploymentPerApplicationNode(t *testing.T) {
	runner := &fakeRunner{applicationNodes: true}
	plugin := Plugin{Runner: runner, StabilityChecks: 1}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","manifestSetRef":"art_manifests","workloadTargetsRef":"art_workloads","serviceEndpointsRef":"art_endpoints"}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = trafficEntrypointArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["trafficProxies"] != "2 application nodes" {
		t.Fatalf("traffic entrypoint precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(runner.appliedManifest, "kind: DaemonSet") || !strings.Contains(runner.appliedManifest, "name: gateway-node-a") || !strings.Contains(runner.appliedManifest, "name: gateway-node-b") || !strings.Contains(runner.appliedManifest, "app: gateway-node-a") || !strings.Contains(runner.appliedManifest, "component: node-proxy") || !strings.Contains(runner.appliedManifest, "kubephos.dev/traffic-entrypoint: node-proxy") || !strings.Contains(runner.appliedManifest, "topology.kubernetes.io/zone: zone-a") || !strings.Contains(runner.appliedManifest, "externalTrafficPolicy: Local") {
		t.Fatalf("unexpected runtime manifest:\n%s", runner.appliedManifest)
	}
	health, err = plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["trafficProxies"] != "2 nodes covered" {
		t.Fatalf("traffic entrypoint verification failed %#v %v", health, err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.ApplicationDeployment.Spec.Workloads) != 2 || decoded.ApplicationDeployment.Spec.Workloads[0].Name != "gateway-node-a" || decoded.ApplicationDeployment.Spec.Workloads[0].NodeName != "node-a" || decoded.ApplicationDeployment.Spec.Workloads[0].NodeZone != "zone-a" || decoded.ApplicationDeployment.Spec.Workloads[1].Name != "gateway-node-b" || decoded.ApplicationDeployment.Spec.Workloads[1].NodeName != "node-b" || decoded.ApplicationDeployment.Spec.Workloads[1].NodeZone != "zone-b" {
		t.Fatalf("unexpected runtime workload contract %#v", decoded.ApplicationDeployment.Spec.Workloads)
	}
}

func testArtifacts() map[string]domain.ResolvedArtifact {
	certificate := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + certificate + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: frontend\nspec:\n  selector:\n    matchLabels:\n      app: frontend\n  template:\n    metadata:\n      labels:\n        app: frontend\n    spec:\n      containers:\n      - name: frontend\n        image: example.invalid/frontend@sha256:" + strings.Repeat("a", 64) + "\n---\napiVersion: v1\nkind: Service\nmetadata:\n  name: frontend\nspec:\n  selector:\n    app: frontend\n  ports:\n  - port: 80\n"
	digest := sha256.Sum256([]byte(manifest))
	manifests, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ManifestSet", "metadata": map[string]string{"applicationRef": "app:example.frontend", "digest": "sha256:" + hex.EncodeToString(digest[:])}, "spec": map[string]any{"renderer": "plain-yaml", "content": manifest, "source": map[string]string{"type": "git", "repository": "https://example.invalid/app.git", "revision": strings.Repeat("a", 40), "path": "deploy", "entrypoint": "app.yaml"}}})
	workloads := json.RawMessage(`[{"id":"frontend","apiVersion":"apps/v1","kind":"Deployment","name":"frontend","selector":{"app":"frontend"},"traits":["scalable","schedulable"]}]`)
	endpoints := json.RawMessage(`[{"id":"frontend-http","component":"frontend","service":"frontend","port":80,"protocol":"http","path":"/"}]`)
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "manifest-set": {Value: manifests}, "workload-targets": {Value: workloads}, "service-endpoints": {Value: endpoints}}
}

func trafficEntrypointArtifacts() map[string]domain.ResolvedArtifact {
	artifacts := testArtifacts()
	manifest := `apiVersion: v1
kind: ConfigMap
metadata:
  name: node-proxy
data:
  default.conf: proxy
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: node-proxy
  labels: {group: example, component: node-proxy, app: node-proxy}
spec:
  selector: {matchLabels: {app: node-proxy}}
  template:
    metadata:
      labels: {group: example, component: node-proxy, app: node-proxy}
    spec:
      nodeSelector: {kubephos.dev/role: application}
      containers:
        - {name: nginx, image: "nginx:1.27-alpine"}
---
apiVersion: v1
kind: Service
metadata:
  name: node-proxy
spec:
  type: NodePort
  externalTrafficPolicy: Local
  selector: {app: node-proxy}
  ports: [{port: 80, targetPort: 8080}]
`
	digest := sha256.Sum256([]byte(manifest))
	manifests, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ManifestSet", "metadata": map[string]string{"applicationRef": "app:example.proxy", "digest": "sha256:" + hex.EncodeToString(digest[:])}, "spec": map[string]any{"renderer": "plain-yaml", "content": manifest, "source": map[string]string{"type": "generated", "repository": "https://example.invalid/app.git", "revision": strings.Repeat("a", 40), "path": ".", "entrypoint": "generated.yaml"}}})
	artifacts["manifest-set"] = domain.ResolvedArtifact{Value: manifests}
	artifacts["workload-targets"] = domain.ResolvedArtifact{Value: json.RawMessage(`[{"id":"node-proxy","apiVersion":"apps/v1","kind":"DaemonSet","name":"node-proxy","selector":{"app":"node-proxy"},"traits":["traffic-entrypoint"]}]`)}
	artifacts["service-endpoints"] = domain.ResolvedArtifact{Value: json.RawMessage(`[{"id":"node-proxy-http","component":"node-proxy","service":"node-proxy","port":80,"protocol":"http","path":"/"}]`)}
	return artifacts
}

func discardLog(string, string) error { return nil }

type fakeRunner struct {
	installed        bool
	marker           string
	controlInstalled bool
	controlMarker    string
	readyWaits       int
	failReadyAt      int
	applicationNodes bool
	appliedManifest  string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "auth can-i") {
		return "yes\n", nil
	}
	if strings.HasPrefix(command, "get namespace ") && strings.Contains(command, "jsonpath=") {
		if strings.Contains(command, "kubephos-control-") {
			if !runner.controlInstalled {
				return "", nil
			}
			return runner.controlMarker, nil
		}
		if !runner.installed {
			return "", nil
		}
		return runner.marker, nil
	}
	if command == "api-resources --namespaced=false -o wide" {
		return "NAME SHORTNAMES APIVERSION NAMESPACED KIND VERBS\nnamespaces ns v1 false Namespace [create delete get]\nclusterroles rbac.authorization.k8s.io/v1 false ClusterRole [create delete get]\n", nil
	}
	if strings.HasPrefix(command, "apply --dry-run=server") {
		return "accepted", nil
	}
	if command == "apply -f -" && strings.Contains(string(stdin), `"Namespace"`) {
		var namespace struct {
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(stdin, &namespace)
		if strings.HasPrefix(namespace.Metadata.Name, "kubephos-control-") {
			runner.controlInstalled = true
			runner.controlMarker = namespace.Metadata.Annotations[ownershipKey]
		} else {
			runner.installed = true
			runner.marker = namespace.Metadata.Annotations[ownershipKey]
		}
		return "created", nil
	}
	if strings.HasPrefix(command, "rollout status deployment/kubephos-pod-scheduling ") {
		return "ready", nil
	}
	if strings.HasPrefix(command, "apply --namespace ") {
		runner.appliedManifest += "\n---\n" + string(stdin)
		return "created", nil
	}
	if command == "get nodes -l kubephos.dev/role=application -o json" && runner.applicationNodes {
		return `{"items":[{"metadata":{"name":"node-b","labels":{"kubernetes.io/hostname":"node-b","topology.kubernetes.io/zone":"zone-b"}},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}},{"metadata":{"name":"node-a","labels":{"kubernetes.io/hostname":"node-a","topology.kubernetes.io/zone":"zone-a"}},"spec":{},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`, nil
	}
	if strings.HasPrefix(command, "get deployment/frontend ") {
		return "deployment.apps/frontend", nil
	}
	if strings.HasPrefix(command, "get deployment/gateway-node-") {
		return "deployment.apps/proxy", nil
	}
	if strings.HasPrefix(command, "get pods ") && strings.HasSuffix(command, "-o json") {
		if strings.Contains(command, "gateway-node-a") {
			return `{"items":[{"spec":{"nodeName":"node-a"}}]}`, nil
		}
		if strings.Contains(command, "gateway-node-b") {
			return `{"items":[{"spec":{"nodeName":"node-b"}}]}`, nil
		}
	}
	if strings.HasPrefix(command, "get pods ") {
		return "pod/frontend-123", nil
	}
	if strings.HasPrefix(command, "wait --for=condition=PodScheduled pod ") {
		return "scheduled", nil
	}
	if strings.HasPrefix(command, "wait --for=condition=Ready pod ") {
		runner.readyWaits++
		if runner.failReadyAt > 0 && runner.readyWaits == runner.failReadyAt {
			return "", errors.New("pod lost readiness")
		}
		return "ready", nil
	}
	if strings.HasPrefix(command, "get endpoints frontend ") {
		return "10.42.0.12", nil
	}
	if strings.HasPrefix(command, "get endpoints node-proxy ") {
		return "10.42.0.21 10.42.1.22", nil
	}
	if strings.HasPrefix(command, "get --raw=/api/v1/namespaces/") {
		return "healthy", nil
	}
	if strings.HasPrefix(command, "delete namespace ") {
		if strings.Contains(command, "kubephos-control-") {
			runner.controlInstalled = false
		} else {
			runner.installed = false
		}
		return "deleted", nil
	}
	if strings.HasPrefix(command, "get namespace ") {
		if strings.Contains(command, "kubephos-control-") {
			if runner.controlInstalled {
				return "present", nil
			}
			return "", nil
		}
		if runner.installed {
			return "present", nil
		}
		return "", nil
	}
	return "", errors.New("unexpected command: " + command)
}

type readinessRunner func(context.Context, string, []byte, ...string) (string, error)

func (runner readinessRunner) Run(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	return runner(ctx, kubeconfig, stdin, args...)
}

func TestReadinessReselectsPodsAfterEviction(t *testing.T) {
	waits, lists := 0, 0
	runner := readinessRunner(func(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
		switch args[0] {
		case "wait":
			waits++
			if waits == 1 {
				return "pod/s6-ready condition met", errors.New(`Error from server (NotFound): pods "s6-evicted" not found`)
			}
			return "pod/s6-replacement condition met", nil
		case "get":
			lists++
			return "pod/s6-replacement", nil
		default:
			t.Fatalf("unexpected command %v", args)
			return "", nil
		}
	})
	if _, err := waitSelectedPodsReady(context.Background(), runner, "config", "namespace", "app=s6"); err != nil {
		t.Fatal(err)
	}
	if waits != 2 || lists != 1 {
		t.Fatalf("expected current pods to be reselected: waits=%d lists=%d", waits, lists)
	}
}

func TestReadinessDoesNotRetryOtherFailures(t *testing.T) {
	for _, message := range []string{"pod lost readiness", "Error from server (Forbidden): pods is forbidden", "Error from server (NotFound): namespaces missing not found"} {
		t.Run(message, func(t *testing.T) {
			calls := 0
			runner := readinessRunner(func(_ context.Context, _ string, _ []byte, _ ...string) (string, error) {
				calls++
				return "", errors.New(message)
			})
			if _, err := waitSelectedPodsReady(context.Background(), runner, "config", "namespace", "app=s6"); err == nil || calls != 1 {
				t.Fatalf("real failures must remain failures: err=%v calls=%d", err, calls)
			}
		})
	}
}

func TestReadinessEvictionRetryHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	runner := readinessRunner(func(_ context.Context, _ string, _ []byte, _ ...string) (string, error) {
		return "", errors.New(`Error from server (NotFound): pods "evicted" not found`)
	})
	if _, err := waitSelectedPodsReady(ctx, runner, "config", "namespace", "app=s6"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected bounded retries, got %v", err)
	}
}

func TestTrafficEntrypointPinningAfterReadiness(t *testing.T) {
	for _, tc := range []struct {
		name          string
		assigned      string
		assignmentErr error
		wantError     string
	}{
		{name: "scheduled during readiness", assigned: `{"items":[{"spec":{"nodeName":"node-a"}}]}`},
		{name: "wrong node", assigned: `{"items":[{"spec":{"nodeName":"node-b"}}]}`, wantError: "observed assignments"},
		{name: "multiple pods", assigned: `{"items":[{"spec":{"nodeName":"node-a"}},{"spec":{"nodeName":"node-a"}}]}`, wantError: "observed assignments"},
		{name: "additional unscheduled pod", assigned: `{"items":[{"spec":{"nodeName":"node-a"}},{"spec":{}}]}`, wantError: "observed assignments"},
		{name: "no pods", assigned: `{"items":[]}`, wantError: "observed assignments"},
		{name: "invalid response", assigned: "invalid", wantError: "decode traffic entrypoint"},
		{name: "API failure", assignmentErr: errors.New("forbidden"), wantError: "forbidden"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready := false
			runner := readinessRunner(func(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
				command := strings.Join(args, " ")
				if args[0] == "wait" {
					ready = true
					return "ready", nil
				}
				if strings.HasSuffix(command, "-o json") {
					if !ready {
						t.Error("pod assignments checked before readiness")
						return "", nil
					}
					return tc.assigned, tc.assignmentErr
				}
				return "pod/gateway-123", nil
			})
			err := verifyWorkloads(context.Background(), runner, "config", "namespace", []workloadTarget{{ID: "gateway", Kind: "Deployment", Name: "gateway-node-a", NodeName: "node-a", Selector: map[string]string{proxyInstanceLabel: "gateway-node-a"}}}, discardLog)
			if tc.wantError == "" && err != nil {
				t.Fatal(err)
			}
			if tc.wantError != "" && (err == nil || !strings.Contains(err.Error(), tc.wantError)) {
				t.Fatalf("expected %q, got %v", tc.wantError, err)
			}
		})
	}
}

func TestReadinessWaitsForControllerToCreatePods(t *testing.T) {
	waits, lists := 0, 0
	runner := readinessRunner(func(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
		switch args[0] {
		case "wait":
			waits++
			if waits == 1 {
				return "", errors.New("kubectl wait: error: no matching resources found")
			}
			return "ready", nil
		case "get":
			lists++
			return "pod/new-gateway", nil
		default:
			t.Fatalf("unexpected command %v", args)
			return "", nil
		}
	})
	if _, err := waitSelectedPodsReady(context.Background(), runner, "config", "namespace", "app=gateway"); err != nil {
		t.Fatal(err)
	}
	if waits != 2 || lists != 1 {
		t.Fatalf("waits=%d lists=%d, want 2 and 1", waits, lists)
	}
}

func TestReadinessMissingPodsHonorsDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		return "", errors.New("kubectl wait: error: no matching resources found")
	})
	if _, err := waitSelectedPodsReady(ctx, runner, "config", "namespace", "app=gateway"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline, got %v", err)
	}
}

func TestLocalRunnerPreservesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (localRunner{}).Run(ctx, "", nil, "get", "pods")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestWorkloadVerificationCancelsPendingWork(t *testing.T) {
	// A canceled caller must stop all concurrent workload commands.
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	stopped := make(chan struct{})
	runner := readinessRunner(func(ctx context.Context, _ string, _ []byte, _ ...string) (string, error) {
		close(started)
		<-ctx.Done()
		close(stopped)
		return "", ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		done <- verifyWorkloads(ctx, runner, "config", "namespace", []workloadTarget{{ID: "gateway", Kind: "Deployment", Name: "gateway"}}, discardLog)
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("workload command was not canceled")
	}
}
