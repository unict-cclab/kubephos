package managedplatform

import (
	"strings"
	"testing"
)

func TestCiliumPlatformOmitsIstioAndPublishesHubble(t *testing.T) {
	content := string(networkObservabilityManifest("test", "cilium"))
	if strings.Contains(content, "telemetry.istio.io") {
		t.Fatal("Cilium installs Istio telemetry")
	}
	value := managedPlatformCapability{}
	value.Spec.Components = []managedComponent{{Name: "istio"}, {Name: "kiali"}, {Name: "mentat"}}
	value.Spec.Endpoints = []managedEndpoint{{Name: "kiali"}, {Name: "headlamp"}}
	actual := ciliumPlatformCapability(value)
	hubble := false
	for _, component := range actual.Spec.Components {
		if component.Name == "istio" || component.Name == "kiali" {
			t.Fatalf("incompatible component: %+v", component)
		}
	}
	for _, endpoint := range actual.Spec.Endpoints {
		if endpoint.Name == "kiali" {
			t.Fatal("Kiali endpoint retained")
		}
		if endpoint.Name == "hubble" && endpoint.NodePort == 31235 {
			hubble = true
		}
	}
	if !hubble {
		t.Fatal("Hubble endpoint missing")
	}
	for _, namespace := range platformNamespaces("cilium") {
		if namespace == istioNamespace {
			t.Fatal("Istio namespace retained")
		}
	}
}
