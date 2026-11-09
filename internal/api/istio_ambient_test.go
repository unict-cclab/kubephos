package api

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/plugins"
	managedplatform "kubephos.dev/kubephos/plugins/managed-platform/runtime"
)

func TestIstioAmbientStageTargetsTheExperimentCluster(t *testing.T) {
	server := &Server{registry: plugins.NewRegistry(manifestOnlyPlugin{managedplatform.AmbientPlugin{}.Manifest()})}
	sources := map[string]experimentArtifactSource{artifactContractKey("ClusterConnection", "v1alpha1"): {ExternalID: "art_cluster"}}
	stage, failure := server.istioAmbientStage(sources, "catalog:app")
	if failure != nil {
		t.Fatalf("stage generation failed: %#v", failure)
	}
	var spec map[string]string
	if err := json.Unmarshal(stage.Spec, &spec); err != nil || spec["clusterConnectionRef"] != "art_cluster" || stage.PluginID != "io.kubephos.platform.istio-ambient" {
		t.Fatalf("unexpected ambient stage: %+v", stage)
	}
	if _, failure := (&Server{registry: plugins.NewRegistry()}).istioAmbientStage(sources, "catalog:app"); failure == nil || failure.code != "capability_unavailable" {
		t.Fatal("a missing ambient plugin was not reported")
	}
}

func TestIstioDataPlaneValidation(t *testing.T) {
	on := true
	for name, test := range map[string]struct {
		definition experimentConfigurationDefinitionInput
		valid      bool
	}{
		"default":           {experimentConfigurationDefinitionInput{}, true},
		"sidecar":           {experimentConfigurationDefinitionInput{IstioDataPlane: "sidecar", IstioInjection: &on}, true},
		"ambient":           {experimentConfigurationDefinitionInput{IstioDataPlane: "ambient"}, true},
		"ambient injection": {experimentConfigurationDefinitionInput{IstioDataPlane: "ambient", IstioInjection: &on}, false},
		"ambient overrides": {experimentConfigurationDefinitionInput{IstioDataPlane: "ambient", IstioInjectionOverrides: map[string]bool{"api": false}}, false},
		"ambient external":  {experimentConfigurationDefinitionInput{IstioDataPlane: "ambient", DestinationMode: "external"}, false},
		"unknown":           {experimentConfigurationDefinitionInput{IstioDataPlane: "proxyless"}, false},
	} {
		if failure := validateIstioDataPlane(test.definition); (failure == nil) != test.valid {
			t.Errorf("%s: expected valid=%v, got %#v", name, test.valid, failure)
		}
	}
}
