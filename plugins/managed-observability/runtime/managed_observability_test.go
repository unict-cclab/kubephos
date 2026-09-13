package managedobservability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestManagedObservabilityLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || len(plan.Steps[0].Outputs) != 2 || plan.Steps[0].Outputs[0].Type != "ObservabilityCapability" || plan.Steps[0].Outputs[0].Sensitive || !plan.Steps[0].Outputs[1].Sensitive {
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
	if decoded.ObservabilityCapability.Metadata.Version != chartVersion+"+mon-agent."+monAgentVersion || decoded.ObservabilityCapability.Spec.MonAgent.Status != "ready" || len(decoded.ObservabilityCapability.Spec.Endpoints) != 2 || decoded.GrafanaCredential.Spec.Password == "" || decoded.GrafanaCredential.Spec.Service != "kubephos-observability-grafana" {
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

func TestValidationRejectsInvalidProfile(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"clusterConnectionRef":"art_same","storageClassCapabilityRef":"art_same","scrapeInterval":"15s","retention":"7d"}`),
		json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"1m","retention":"7d"}`),
		json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"forever"}`),
	}
	for _, value := range tests {
		if (Plugin{}).Validate(context.Background(), Invocation{Input: value}).Valid {
			t.Fatalf("expected rejection for %s", value)
		}
	}
}

func TestPrecheckRejectsCrossClusterStorage(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{}, Fetcher: fakeFetcher{}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	var storage storageClassCapability
	if err := json.Unmarshal(step.ResolvedInputs["storage-class-capability"].Value, &storage); err != nil {
		t.Fatal(err)
	}
	storage.Spec.ClusterServer = "https://10.10.0.99:6443"
	value, _ := json.Marshal(storage)
	step.ResolvedInputs["storage-class-capability"] = domain.ResolvedArtifact{Value: value}
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected cross-cluster rejection %#v %v", health, err)
	}
}

func TestCleanupRejectsForeignOwnership(t *testing.T) {
	runner := &fakeRunner{installed: true, marker: "foreign"}
	plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.Cleanup = true
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected ownership rejection %#v %v", health, err)
	}
}

func TestCleanupAcceptsUnmarkedCRDsUnderOwnedNamespace(t *testing.T) {
	empty := ""
	runner := &fakeRunner{installed: true, crdMarker: &empty}
	plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		t.Fatal(err)
	}
	runner.marker = input.Marker
	step.Cleanup = true
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("expected owned cleanup acceptance %#v %v", health, err)
	}
}

func TestPlanDoesNotContainServiceCredential(t *testing.T) {
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","storageClassCapabilityRef":"art_storage","scrapeInterval":"15s","retention":"7d"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(plan)
	if strings.Contains(string(value), "adminPassword") || strings.Contains(string(value), "client-key-data") {
		t.Fatalf("plan contains credential material: %s", value)
	}
}

func TestManagedValuesContainOnlySupportedControls(t *testing.T) {
	values, err := managedValues(stepInput{Spec: Spec{ScrapeInterval: "10s", Retention: "15d"}}, "shared-storage", "secret")
	if err != nil {
		t.Fatal(err)
	}
	text := string(values)
	for _, expected := range []string{"scrapeInterval: 10s", "evaluationInterval: 10s", "retention: 15d", "storageClassName: shared-storage", "adminPassword: secret", "initChownData:", "enabled: false"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("managed value %q is missing from %s", expected, text)
		}
	}
}

func TestHTTPFetcherVerifiesChartDigest(t *testing.T) {
	original := []byte("chart")
	digest := sha256.Sum256(original)
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(original))}, nil
	})}
	fetcher := httpChartFetcher{client: client}
	response, err := client.Get("https://example.invalid/chart")
	if err != nil {
		t.Fatal(err)
	}
	value, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if hex.EncodeToString(digest[:]) != hex.EncodeToString(sha256Value(value)) {
		t.Fatal("test transport changed chart content")
	}
	if _, err := fetcher.Fetch(context.Background()); err == nil {
		t.Fatal("expected managed digest mismatch")
	}
}

func TestChartReleaseIsPinned(t *testing.T) {
	if !strings.Contains(chartURL, "kube-prometheus-stack-"+chartVersion) || len(chartDigest) != 64 {
		t.Fatal("managed chart is not pinned")
	}
}

func testArtifacts() map[string]domain.ResolvedArtifact {
	cert := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + cert + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	storage := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"StorageClassCapability","metadata":{"name":"shared-storage","version":"v4.13.4"},"spec":{"clusterServer":"https://10.10.0.10:6443","provisioner":"nfs.csi.k8s.io","directoryPermissions":"0777","expansion":true}}`)
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "storage-class-capability": {Value: storage}}
}

func discardLog(string, string) error { return nil }

func sha256Value(value []byte) []byte {
	digest := sha256.Sum256(value)
	return digest[:]
}

type fakeFetcher struct{}

func (fakeFetcher) Fetch(context.Context) ([]byte, error) { return []byte("chart"), nil }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type fakeRunner struct {
	installed bool
	marker    string
	crdMarker *string
}

func (runner *fakeRunner) Kubectl(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "get --raw=/api/v1/namespaces/") {
		if !runner.installed {
			return "", errors.New("not found")
		}
		return "healthy", nil
	}
	if strings.HasPrefix(command, "auth can-i") {
		return "yes\n", nil
	}
	if strings.HasPrefix(command, "get storageclass shared-storage -o jsonpath={.provisioner}") {
		return "nfs.csi.k8s.io", nil
	}
	if command == "apply -f -" && strings.Contains(string(stdin), `"Namespace"`) {
		runner.installed = true
		var object struct {
			Metadata struct {
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		_ = json.Unmarshal(stdin, &object)
		runner.marker = object.Metadata.Annotations[ownershipKey]
		return "applied", nil
	}
	if command == "apply -f -" && strings.Contains(string(stdin), monAgentName) {
		return "applied", nil
	}
	if command == "rollout status deployment/"+monAgentName+" -n "+namespace+" --timeout=5m" {
		return "ready", nil
	}
	if strings.HasPrefix(command, "annotate --overwrite crd/") {
		return "annotated", nil
	}
	if command == "get services -n "+namespace+" -o json" {
		return `{"items":[{"metadata":{"name":"kubephos-observability-prometheus-node-exporter","labels":{"app.kubernetes.io/name":"prometheus-node-exporter"}},"spec":{"ports":[{"name":"metrics","port":9100}]}},{"metadata":{"name":"kubephos-observability-grafana","labels":{"app.kubernetes.io/name":"grafana"}},"spec":{"type":"NodePort","ports":[{"name":"service","port":80,"nodePort":32000}]}},{"metadata":{"name":"kubephos-observability-kube-prometheus-prometheus","labels":{"app.kubernetes.io/name":"prometheus"}},"spec":{"type":"NodePort","ports":[{"name":"http-web","port":9090,"nodePort":32090}]}}]}`, nil
	}
	if command == "get pods -n "+namespace+" -o json" {
		return readyPods(), nil
	}
	if strings.HasPrefix(command, "get pvc -n "+namespace) {
		return "Bound\nBound\n", nil
	}
	if strings.Contains(command, "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}") {
		if !runner.installed {
			return "", errors.New("not found")
		}
		if strings.HasPrefix(command, "get crd ") && runner.crdMarker != nil {
			return *runner.crdMarker, nil
		}
		return runner.marker, nil
	}
	if strings.HasPrefix(command, "delete crd ") {
		return "deleted", nil
	}
	if strings.HasPrefix(command, "delete clusterrole/") {
		return "deleted", nil
	}
	if strings.HasPrefix(command, "delete namespace ") {
		runner.installed = false
		return "deleted", nil
	}
	if strings.HasPrefix(command, "get namespace ") || strings.HasPrefix(command, "get crd ") {
		if runner.installed {
			return "present", nil
		}
		return "", errors.New("not found")
	}
	return "", errors.New("unexpected kubectl command: " + command)
}

func (runner *fakeRunner) Helm(_ context.Context, _ string, chart, values []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if strings.HasPrefix(command, "upgrade --install") {
		if len(chart) == 0 || !strings.Contains(string(values), "adminPassword") {
			return "", errors.New("chart or values missing")
		}
		runner.installed = true
		return "installed", nil
	}
	if strings.HasPrefix(command, "status ") {
		if !runner.installed {
			return "", errors.New("not found")
		}
		return "deployed", nil
	}
	if strings.HasPrefix(command, "uninstall ") {
		return "uninstalled", nil
	}
	return "", errors.New("unexpected helm command: " + command)
}

func readyPods() string {
	items := make([]map[string]any, 5)
	for index := range items {
		items[index] = map[string]any{"metadata": map[string]string{"name": fmt.Sprintf("pod-%d", index)}, "status": map[string]any{"phase": "Running", "conditions": []map[string]string{{"type": "Ready", "status": "True"}}}}
	}
	value, _ := json.Marshal(map[string]any{"items": items})
	return string(value)
}
