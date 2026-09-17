package managedplatform

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
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

func TestManagedSupportManifestsAndValuesAreValidYAML(t *testing.T) {
	prometheus := prometheusService{Name: "observability-prometheus", Namespace: observabilityNamespace, Port: 9090}
	values := [][]byte{lokiValues(), alloyValues(), kialiValues(prometheus), extendedObservabilityManifest("marker")}
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
