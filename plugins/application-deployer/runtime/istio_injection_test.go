package applicationdeployer

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestIstioInjectionPreservesLegacyAndSeparatesOverrides(t *testing.T) {
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    metadata:\n      annotations:\n        sidecar.istio.io/inject: 'false'\n    spec:\n      containers: []\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\nspec:\n  template:\n    metadata: {}\n    spec:\n      containers: []\n---\napiVersion: batch/v1\nkind: Job\nmetadata:\n  name: bootstrap\nspec:\n  template:\n    metadata:\n      annotations:\n        sidecar.istio.io/inject: 'false'\n    spec:\n      containers: []\n"
	targets := []workloadTarget{{ID: "api", Kind: "Deployment", Name: "api"}, {ID: "web", Kind: "Deployment", Name: "web"}}
	unchanged, err := configureIstioInjection(manifest, targets, nil, nil)
	if err != nil || unchanged != manifest {
		t.Fatalf("legacy manifest changed: %v", err)
	}
	off := false
	result, err := configureIstioInjection(manifest, targets, &off, map[string]bool{"api": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(result, `sidecar.istio.io/inject: "true"`)+strings.Count(result, "sidecar.istio.io/inject: 'true'") != 2 {
		t.Fatal("service override did not enable its annotation and label")
	}
	if strings.Contains(result, "name: bootstrap\n") && !strings.Contains(result, "sidecar.istio.io/inject") {
		t.Fatal("bootstrap opt-out lost")
	}
	if namespaceIstioInjection(stepInput{Spec: Spec{IstioInjection: &off, IstioInjectionOverrides: map[string]bool{"api": true}}}, clusterConnection{}, nil) != "enabled" {
		t.Fatal("namespace prevents explicit service opt-in")
	}
	if _, err := configureIstioInjection(manifest, targets, &off, map[string]bool{"missing": true}); err == nil {
		t.Fatal("unknown service accepted")
	}
}

func TestIstioNamespaceDefaultsAndBootstrapOptOut(t *testing.T) {
	for _, tt := range []struct {
		name, network, protocol string
		want                    string
	}{
		{name: "legacy HTTP", protocol: "http", want: "enabled"},
		{name: "legacy TCP", protocol: "tcp", want: "disabled"},
		{name: "no endpoints", want: "enabled"},
		{name: "Cilium", network: "cilium", protocol: "http", want: "disabled"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cluster := clusterConnection{}
			cluster.Spec.Networking = tt.network
			var endpoints []serviceEndpoint
			if tt.protocol != "" {
				endpoints = []serviceEndpoint{{Protocol: tt.protocol}}
			}
			if got := namespaceIstioInjection(stepInput{}, cluster, endpoints); got != tt.want {
				t.Fatalf("namespace injection=%q, want %q", got, tt.want)
			}
		})
	}
	content := "apiVersion: batch/v1\nkind: Job\nmetadata:\n  name: seed\nspec:\n  template:\n    metadata:\n      annotations:\n        sidecar.istio.io/inject: 'false'\n    spec:\n      containers: []\n"
	enabled := true
	result, err := configureIstioInjection(content, nil, &enabled, nil)
	if err != nil {
		t.Fatal(err)
	}
	var resource map[string]any
	if err := yaml.Unmarshal([]byte(result), &resource); err != nil {
		t.Fatal(err)
	}
	metadata := resource["spec"].(map[string]any)["template"].(map[string]any)["metadata"].(map[string]any)
	if metadata["annotations"].(map[string]any)["sidecar.istio.io/inject"] != "false" {
		t.Fatal("global opt-in changed bootstrap Job opt-out")
	}
}
