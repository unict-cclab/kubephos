package nfscsi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestKubernetesSharedStorageLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}, Probe: fakeProbe{}}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"shared-storage"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || len(plan.Steps[0].Outputs) != 1 || plan.Steps[0].Outputs[0].Type != "StorageClassCapability" || plan.Steps[0].Outputs[0].Sensitive {
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
	capability := decoded.StorageClassCapability
	if capability.Metadata.Version != driverVersion || capability.Spec.Provisioner != provisioner || capability.Spec.Server != "10.10.0.12" || capability.Spec.ExportPath != "/srv/kubephos" || !capability.Spec.Expansion {
		t.Fatalf("unexpected capability %#v", capability)
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

func TestValidationRejectsInvalidInputs(t *testing.T) {
	tests := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"clusterConnectionRef":"art_same","sharedStorageEndpointRef":"art_same","storageClassName":"shared-storage"}`),
		json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"Invalid_Name"}`),
	}
	for _, value := range tests {
		if (Plugin{}).Validate(context.Background(), Invocation{Input: value}).Valid {
			t.Fatalf("expected rejection for %s", value)
		}
	}
}

func TestPrecheckRejectsMalformedKubeconfigAndStorage(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{}, Fetcher: fakeFetcher{}, Probe: fakeProbe{}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"shared-storage"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	var cluster clusterConnection
	if err := json.Unmarshal(step.ResolvedInputs["cluster-connection"].Value, &cluster); err != nil {
		t.Fatal(err)
	}
	cluster.Spec.Kubeconfig = "apiVersion: v1\nkind: Config\n"
	value, _ := json.Marshal(cluster)
	step.ResolvedInputs["cluster-connection"] = domain.ResolvedArtifact{Value: value}
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected malformed kubeconfig rejection %#v %v", health, err)
	}
	step.ResolvedInputs = testArtifacts()
	var storage sharedStorageEndpoint
	if err := json.Unmarshal(step.ResolvedInputs["shared-storage-endpoint"].Value, &storage); err != nil {
		t.Fatal(err)
	}
	storage.Spec.ExportPath = "/srv/../etc"
	value, _ = json.Marshal(storage)
	step.ResolvedInputs["shared-storage-endpoint"] = domain.ResolvedArtifact{Value: value}
	health, err = plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected storage rejection %#v %v", health, err)
	}
}

func TestCleanupRejectsForeignOwnership(t *testing.T) {
	runner := &fakeRunner{installed: true, marker: "someone-else"}
	plugin := Plugin{Runner: runner, Fetcher: fakeFetcher{}, Probe: fakeProbe{}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"shared-storage"}`))
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

func TestPlanContainsNoKubeconfig(t *testing.T) {
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","sharedStorageEndpointRef":"art_storage","storageClassName":"shared-storage"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, _ := json.Marshal(plan)
	if strings.Contains(string(value), "client-certificate-data") || strings.Contains(string(value), "client-key-data") {
		t.Fatalf("plan contains credential material: %s", value)
	}
}

func TestHTTPFetcherVerifiesDigest(t *testing.T) {
	digest := sha256.Sum256([]byte("manifest"))
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte("manifest")))}, nil
	})}
	fetcher := httpManifestFetcher{client: client}
	if _, err := fetcher.Fetch(context.Background(), manifestSource{URL: "https://example.invalid/manifest", Digest: hex.EncodeToString(digest[:])}); err != nil {
		t.Fatal(err)
	}
	if _, err := fetcher.Fetch(context.Background(), manifestSource{URL: "https://example.invalid/manifest", Digest: strings.Repeat("0", 64)}); err == nil {
		t.Fatal("expected digest mismatch")
	}
}

func TestDriverReleaseIsPinned(t *testing.T) {
	if len(driverManifests) != 4 {
		t.Fatalf("unexpected manifest count %d", len(driverManifests))
	}
	for _, source := range driverManifests {
		if !strings.Contains(source.URL, driverCommit) || !strings.Contains(source.URL, "/deploy/"+driverVersion+"/") || len(source.Digest) != 64 {
			t.Fatalf("manifest is not pinned %#v", source)
		}
	}
}

func testArtifacts() map[string]domain.ResolvedArtifact {
	cert := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + cert + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	storage := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"SharedStorageEndpoint","metadata":{"name":"shared-storage","version":"1.0"},"spec":{"protocol":"nfs","server":"10.10.0.12","exportPath":"/srv/kubephos","clientCIDR":"10.10.0.0/24","mountOptions":["nfsvers=4.1"]}}`)
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "shared-storage-endpoint": {Value: storage}}
}

func discardLog(string, string) error { return nil }

type fakeFetcher struct{}

func (fakeFetcher) Fetch(context.Context, manifestSource) ([]byte, error) {
	return []byte(`{"apiVersion":"v1","kind":"List"}`), nil
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type fakeProbe struct{ err error }

func (probe fakeProbe) Check(context.Context, string, string) error { return probe.err }

type fakeRunner struct {
	installed bool
	marker    string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "auth can-i") {
		return "yes\n", nil
	}
	if command == "apply -f -" {
		if strings.Contains(string(stdin), "StorageClass") {
			runner.installed = true
		}
		return "applied", nil
	}
	if strings.HasPrefix(command, "annotate --overwrite") {
		for _, arg := range args {
			if strings.HasPrefix(arg, "kubephos.dev/ownership-marker=") {
				runner.marker = strings.TrimPrefix(arg, "kubephos.dev/ownership-marker=")
				runner.installed = true
			}
		}
		return "annotated", nil
	}
	if strings.HasPrefix(command, "rollout status") || strings.HasPrefix(command, "wait ") {
		if !runner.installed {
			return "", errors.New("absent")
		}
		return "ready", nil
	}
	if strings.HasPrefix(command, "exec pod/storage-probe") {
		return "kubephos-storage-ready\n", nil
	}
	if strings.HasPrefix(command, "delete namespace") {
		return "deleted", nil
	}
	if strings.HasPrefix(command, "delete storageclass") {
		return "deleted", nil
	}
	if command == "delete --ignore-not-found -f -" {
		runner.installed = false
		return "deleted", nil
	}
	if strings.Contains(command, "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}") {
		if !runner.installed {
			return "", errors.New("not found")
		}
		return runner.marker, nil
	}
	if strings.Contains(command, "jsonpath={.provisioner}") {
		return provisioner, nil
	}
	if strings.Contains(command, "jsonpath={.parameters.server}") {
		return "10.10.0.12", nil
	}
	if strings.Contains(command, "jsonpath={.parameters.share}") {
		return "/srv/kubephos", nil
	}
	if strings.HasPrefix(command, "get ") {
		if runner.installed {
			return "present", nil
		}
		return "", errors.New("not found")
	}
	return "", errors.New("unexpected command: " + command)
}
