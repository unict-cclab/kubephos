package managedplatform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
)

func TestCPAPlacementUsesInlinePatch(t *testing.T) {
	patch := []byte(`{"spec":{"template":{"spec":{"nodeSelector":{"kubephos.dev/role":"management"}}}}}`)
	arguments := cpaPlacementArguments(patch)
	if slices.Contains(arguments, "--patch-file") || !slices.Contains(arguments, "--patch") || arguments[len(arguments)-1] != string(patch) {
		t.Fatalf("unexpected patch arguments: %#v", arguments)
	}
}

type namespaceRunner struct {
	value string
	err   error
}

func TestCleanupDoesNotRequireManagedRegistry(t *testing.T) {
	step := domain.PlanStep{
		Input:   json.RawMessage(`{"clusterConnectionRef":"art_cluster","marker":"marker"}`),
		Cleanup: true,
		ResolvedInputs: map[string]domain.ResolvedArtifact{
			"cluster-connection": {Value: json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ClusterConnection","metadata":{"name":"cluster","version":"1"},"spec":{"distribution":"k3s","server":"https://cluster.example.test","kubeconfig":"config"}}`)},
		},
	}
	err := (Plugin{Runner: namespaceRunner{}}).Cleanup(context.Background(), step, nil, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
}

func TestManagedSupportManifestsAndValuesAreValidYAML(t *testing.T) {
	prometheus := prometheusService{Name: "observability-prometheus", Namespace: observabilityNamespace, Port: 9090}
	values := [][]byte{lokiValues(), alloyValues(), kialiValues(prometheus), headlampValues("marker"), extendedObservabilityManifest("marker")}
	support, err := supportManifest("marker", "registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	values = append(values, support)
	for index, value := range values {
		decoder := yaml.NewDecoder(bytes.NewReader(value))
		for {
			var document any
			err := decoder.Decode(&document)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("document %d: %v", index, err)
			}
		}
	}
}

func TestHeadlampIsClusterAdminAndRequiresTokenLogin(t *testing.T) {
	values := headlampValues("marker")
	for _, expected := range [][]byte{
		[]byte("clusterRoleBinding:\n  create: false"),
		[]byte("kind: ClusterRoleBinding"),
		[]byte("name: kubephos-headlamp-cluster-admin"),
		[]byte("kind: ClusterRole\n    name: cluster-admin"),
		[]byte("unsafeUseServiceAccountToken: false"),
		[]byte("secret:\n      create: false"),
		[]byte("registry: ghcr.io"),
		[]byte("repository: headlamp-k8s/headlamp"),
		[]byte("tag: v0.45.0"),
		[]byte("nodePort: 30003"),
		[]byte("kubephos.dev/role: management"),
		[]byte("kubernetes.io/service-account.name: kubephos-headlamp"),
	} {
		if !bytes.Contains(values, expected) {
			t.Fatalf("Headlamp values do not contain %s", expected)
		}
	}
}

func TestReadHeadlampTokenDecodesSecret(t *testing.T) {
	token, err := readHeadlampToken(context.Background(), namespaceRunner{value: "dmlld2VyLXRva2Vu"}, "config")
	if err != nil {
		t.Fatal(err)
	}
	if token != "viewer-token" {
		t.Fatalf("unexpected Headlamp token %q", token)
	}
}

func TestKialiUsesDiscoveredPrometheusService(t *testing.T) {
	prometheus := prometheusService{Name: "observability-prometheus", Namespace: observabilityNamespace, Port: 9090}
	values := kialiValues(prometheus)
	if !bytes.Contains(values, []byte("url: "+prometheus.URL())) {
		t.Fatalf("Kiali values do not contain %s", prometheus.URL())
	}
	for _, expected := range [][]byte{[]byte("provider: jaeger"), []byte("use_grpc: false"), []byte("internal_url: http://jaeger.kubephos-observability.svc.cluster.local:16686"), []byte("internal_url: http://kubephos-observability-grafana.kubephos-observability.svc.cluster.local:80")} {
		if !bytes.Contains(values, expected) {
			t.Fatalf("Kiali values do not contain %s", expected)
		}
	}
	if err := validateKialiPrometheusConfig(string(values), prometheus); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverPrometheusServiceUsesLabelsAndRuntimeName(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"generated-prometheus","labels":{"app.kubernetes.io/name":"prometheus"}},"spec":{"ports":[{"name":"http-web","port":9090},{"name":"reloader-web","port":8080}]}}]}`
	service, err := discoverPrometheusService(context.Background(), namespaceRunner{value: raw}, "config")
	if err != nil {
		t.Fatal(err)
	}
	if service.Name != "generated-prometheus" || service.Port != 9090 || service.Namespace != observabilityNamespace {
		t.Fatalf("unexpected Prometheus service %#v", service)
	}
}

func TestManagedSupportLabelsMentatPeers(t *testing.T) {
	manifest, err := supportManifest("marker", "registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte("app: mentat")) {
		t.Fatal("Mentat peer-discovery label is missing")
	}
}

func TestClusterLensUsesManagedRegistry(t *testing.T) {
	host := "registry.example.test:5443"
	if !validManagedRegistryHost(host) || validManagedRegistryHost("registry.example.test/other") {
		t.Fatal("managed registry host validation is incorrect")
	}
	manifest, err := supportManifest("marker", host)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(manifest, []byte("image: "+host+"/kubephos-dev/cluster-lens:"+clusterLensVersion)) {
		t.Fatal("Cluster Lens image does not use the selected managed registry")
	}
	if !bytes.Contains(manifest, []byte("image: "+host+"/kubephos-dev/mentat:"+mentatVersion)) {
		t.Fatal("Mentat image does not use the selected managed registry")
	}
}

func TestNetworkAnnotationCoverage(t *testing.T) {
	raw := `{"items":[{"metadata":{"name":"node-a","annotations":{"network-latency.node-b":"1.2","network-bandwidth.node-b":"100"}}},{"metadata":{"name":"node-b","annotations":{"network-latency.node-a":"1.4"}}}]}`
	latency, bandwidth, err := networkAnnotationCoverage(raw)
	if err != nil || latency != 2 || bandwidth != 1 {
		t.Fatalf("unexpected coverage latency=%d bandwidth=%d err=%v", latency, bandwidth, err)
	}
}

func (runner namespaceRunner) Kubectl(context.Context, string, []byte, ...string) (string, error) {
	return runner.value, runner.err
}

func (namespaceRunner) Helm(context.Context, string, []byte, []byte, ...string) (string, error) {
	return "", nil
}

func TestNamespaceOwnershipDistinguishesAbsentUnownedAndOwned(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		owned   bool
		present bool
	}{
		{name: "absent"},
		{name: "unowned", value: `{"metadata":{"annotations":{}}}`, present: true},
		{name: "owned", value: `{"metadata":{"annotations":{"kubephos.dev/ownership-marker":"marker"}}}`, owned: true, present: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owned, present, err := namespaceOwnership(context.Background(), namespaceRunner{value: test.value}, "config", "namespace", "marker")
			if err != nil || owned != test.owned || present != test.present {
				t.Fatalf("got owned=%v present=%v err=%v", owned, present, err)
			}
		})
	}
}

func TestNamespaceOwnershipPreservesReadError(t *testing.T) {
	expected := errors.New("read failed")
	_, _, err := namespaceOwnership(context.Background(), namespaceRunner{err: expected}, "config", "namespace", "marker")
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
}

func TestManagementIngressPreservesExistingValues(t *testing.T) {
	raw := []byte(`{"spec":{"valuesContent":"nodeSelector:\n  kubernetes.io/os: linux\nports:\n  web:\n    port: 8081\n"}}`)
	manifest, err := managementIngressManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Spec struct {
			ValuesContent string `json:"valuesContent"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(manifest, &doc); err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(doc.Spec.ValuesContent), &values); err != nil {
		t.Fatal(err)
	}
	selector := values["nodeSelector"].(map[string]any)
	if selector["kubephos.dev/role"] != "management" || selector["kubernetes.io/os"] != "linux" {
		t.Fatalf("bad selector %v", selector)
	}
	if values["ports"].(map[string]any)["web"].(map[string]any)["port"] != 8081 {
		t.Fatal("existing ingress port changed")
	}
}
