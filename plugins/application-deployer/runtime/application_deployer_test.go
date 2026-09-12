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

func testArtifacts() map[string]domain.ResolvedArtifact {
	certificate := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + certificate + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: frontend\nspec:\n  selector:\n    matchLabels:\n      app: frontend\n  template:\n    metadata:\n      labels:\n        app: frontend\n    spec:\n      containers:\n      - name: frontend\n        image: example.invalid/frontend@sha256:" + strings.Repeat("a", 64) + "\n---\napiVersion: v1\nkind: Service\nmetadata:\n  name: frontend\nspec:\n  selector:\n    app: frontend\n  ports:\n  - port: 80\n"
	digest := sha256.Sum256([]byte(manifest))
	manifests, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ManifestSet", "metadata": map[string]string{"applicationRef": "app:example.frontend@1.0.0", "digest": "sha256:" + hex.EncodeToString(digest[:])}, "spec": map[string]any{"renderer": "plain-yaml", "content": manifest, "source": map[string]string{"type": "git", "repository": "https://example.invalid/app.git", "revision": strings.Repeat("a", 40), "path": "deploy", "entrypoint": "app.yaml"}}})
	workloads := json.RawMessage(`[{"id":"frontend","apiVersion":"apps/v1","kind":"Deployment","name":"frontend","selector":{"app":"frontend"},"traits":["scalable","schedulable"]}]`)
	endpoints := json.RawMessage(`[{"id":"frontend-http","component":"frontend","service":"frontend","port":80,"protocol":"http","path":"/"}]`)
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "manifest-set": {Value: manifests}, "workload-targets": {Value: workloads}, "service-endpoints": {Value: endpoints}}
}

func discardLog(string, string) error { return nil }

type fakeRunner struct {
	installed   bool
	marker      string
	readyWaits  int
	failReadyAt int
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
		runner.installed = true
		var namespace struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(stdin, &namespace)
		runner.marker = namespace.Metadata.Annotations[ownershipKey]
		return "created", nil
	}
	if strings.HasPrefix(command, "apply --namespace ") {
		return "created", nil
	}
	if strings.HasPrefix(command, "get deployment/frontend ") {
		return "deployment.apps/frontend", nil
	}
	if strings.HasPrefix(command, "get pods ") {
		return "pod/frontend-123", nil
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
	if strings.HasPrefix(command, "get --raw=/api/v1/namespaces/") {
		return "healthy", nil
	}
	if strings.HasPrefix(command, "delete namespace ") {
		runner.installed = false
		return "deleted", nil
	}
	if strings.HasPrefix(command, "get namespace ") {
		if runner.installed {
			return "present", nil
		}
		return "", nil
	}
	return "", errors.New("unexpected command: " + command)
}
