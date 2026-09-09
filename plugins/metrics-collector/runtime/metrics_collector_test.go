package metricscollector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestCollectorPrecheckExecuteAndVerify(t *testing.T) {
	now := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	runner := &fakeRunner{now: now}
	plugin := Plugin{Runner: runner, Clock: func() time.Time { return now }}
	step := testStep(t)
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	raw, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, raw, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if value.Dataset.Spec.Summary.Metrics != 4 || value.Dataset.Spec.Summary.Series != 4 || value.Dataset.Spec.Summary.Samples != 8 {
		t.Fatalf("unexpected dataset summary %#v", value.Dataset.Spec.Summary)
	}
	if runner.maximumConcurrency() < 2 {
		t.Fatal("expected concurrent metrics queries")
	}
}

func TestCollectorRejectsCrossClusterCapabilityBeforeQuery(t *testing.T) {
	step := testStep(t)
	var capability observabilityCapability
	if err := json.Unmarshal(step.ResolvedInputs["observability-capability"].Value, &capability); err != nil {
		t.Fatal(err)
	}
	capability.Spec.ClusterServer = "https://10.10.0.20:6443"
	value, _ := json.Marshal(capability)
	step.ResolvedInputs["observability-capability"] = domain.ResolvedArtifact{Value: value}
	runner := &fakeRunner{}
	health, err := (Plugin{Runner: runner}).Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy || runner.callCount() != 0 {
		t.Fatalf("expected artifact rejection before external calls %#v %v", health, err)
	}
}

func TestDecodeMatrixRejectsUnorderedSamples(t *testing.T) {
	start := time.Unix(100, 0).UTC()
	end := time.Unix(200, 0).UTC()
	raw := `{"status":"success","data":{"resultType":"matrix","result":[{"metric":{"pod":"one"},"values":[[150,"1"],[140,"2"]]}]}}`
	if _, err := decodeMatrix(raw, metricDefinition{ID: "cpu.cores", Unit: "cores"}, start, end); err == nil {
		t.Fatal("expected unordered samples to fail")
	}
}

func testStep(t *testing.T) domain.PlanStep {
	t.Helper()
	spec := Spec{ClusterConnectionRef: "art_cluster", ApplicationDeploymentRef: "art_deployment", ObservabilityCapabilityRef: "art_observability", Window: "5m", Resolution: "15s"}
	raw, _ := json.Marshal(spec)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	return step
}

func testArtifacts() map[string]domain.ResolvedArtifact {
	certificate := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + certificate + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	deployment, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ApplicationDeployment", "metadata": map[string]string{"name": "kubephos-app-one", "version": "v1alpha1", "ownershipMarker": "application-marker"}, "spec": map[string]string{"applicationRef": "app:dev.example.app@1.0.0", "clusterServer": "https://10.10.0.10:6443", "namespace": "kubephos-app-one"}})
	capability, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ObservabilityCapability", "metadata": map[string]string{"name": "managed-observability", "version": "86.0.0"}, "spec": map[string]any{"clusterServer": "https://10.10.0.10:6443", "namespace": "kubephos-observability", "scrapeInterval": "15s", "retention": "7d", "metricsAPI": "prometheus-v1", "endpoints": []map[string]any{{"name": "prometheus", "serviceName": "kubephos-prometheus", "port": 9090, "scheme": "http"}}}})
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "application-deployment": {Value: deployment}, "observability-capability": {Value: capability}}
}

func discardLog(string, string) error { return nil }

type fakeRunner struct {
	now       time.Time
	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

func (runner *fakeRunner) Run(ctx context.Context, _ string, args ...string) (string, error) {
	command := strings.Join(args, " ")
	runner.mu.Lock()
	runner.calls++
	runner.mu.Unlock()
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "auth can-i get services/proxy ") {
		return "yes\n", nil
	}
	if strings.HasPrefix(command, "get namespace kubephos-app-one ") {
		return "application-marker", nil
	}
	if strings.HasSuffix(command, "/-/ready") {
		return "Prometheus Server is Ready.\n", nil
	}
	if strings.Contains(command, "/api/v1/query?query=vector%281%29") {
		return `{"status":"success","data":{"resultType":"vector","result":[]}}`, nil
	}
	if !strings.Contains(command, "/api/v1/query_range?") {
		return "", errors.New("unexpected command: " + command)
	}
	runner.mu.Lock()
	runner.active++
	if runner.active > runner.maxActive {
		runner.maxActive = runner.active
	}
	runner.mu.Unlock()
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	runner.mu.Lock()
	runner.active--
	runner.mu.Unlock()
	argument := strings.TrimPrefix(args[len(args)-1], "--raw=")
	parsed, err := url.Parse(argument)
	if err != nil {
		return "", err
	}
	query := parsed.Query().Get("query")
	labels := map[string]string{"pod": "one"}
	if strings.Contains(query, "deployment") {
		labels = map[string]string{"deployment": "frontend"}
	}
	start := runner.now.Add(-time.Minute).Unix()
	end := runner.now.Unix()
	response := map[string]any{"status": "success", "data": map[string]any{"resultType": "matrix", "result": []map[string]any{{"metric": labels, "values": [][]any{{start, "1"}, {end, strconv.Itoa(len(query))}}}}}}
	value, _ := json.Marshal(response)
	return string(value), nil
}

func (runner *fakeRunner) maximumConcurrency() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.maxActive
}

func (runner *fakeRunner) callCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls
}
