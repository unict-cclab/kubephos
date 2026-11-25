package api

import (
	"encoding/json"
	"reflect"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

// Bindings used to follow Go map iteration over the schema properties, so the
// same configuration produced differently ordered bindings, placeholder tokens
// and pipeline hashes on every execution.
func TestExperimentArtifactBindingsAreDeterministic(t *testing.T) {
	manifest := plugins.Manifest{ID: "io.kubephos.test", Schema: json.RawMessage(`{"type":"object","properties":{
		"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},
		"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},
		"loadScenarioSetRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadScenarioSet","x-kubephos-artifact-version":"v1alpha1"},
		"loadSessionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"LoadSession","x-kubephos-artifact-version":"v1alpha1"},
		"targetBindingRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"TargetBinding","x-kubephos-artifact-version":"v1alpha1"},
		"resolution":{"type":"string"}}}`)}
	sources := map[string]experimentArtifactSource{
		artifactContractKey("ClusterConnection", "v1alpha1"):     {ExternalID: "art_cluster"},
		artifactContractKey("ApplicationDeployment", "v1alpha1"): {StageID: "deploy"},
		artifactContractKey("LoadScenarioSet", "v1alpha1"):       {StageID: "application"},
		artifactContractKey("LoadSession", "v1alpha1"):           {StageID: "capability-2"},
		artifactContractKey("TargetBinding", "v1alpha1"):         {StageID: "targets-1"},
	}
	expected := []domain.PipelineBinding{{Path: "/applicationDeploymentRef", FromStage: "deploy"}, {Path: "/loadScenarioSetRef", FromStage: "application"}, {Path: "/loadSessionRef", FromStage: "capability-2"}, {Path: "/targetBindingRef", FromStage: "targets-1"}}
	for attempt := 0; attempt < 50; attempt++ {
		spec := map[string]any{"resolution": "15s"}
		bindings, failure := experimentArtifactBindings(spec, manifest, sources, "catalog:demo@1.0.0")
		if failure != nil {
			t.Fatal(failure.message)
		}
		if !reflect.DeepEqual(bindings, expected) || spec["clusterConnectionRef"] != "art_cluster" {
			t.Fatalf("attempt %d: bindings %#v spec %#v", attempt, bindings, spec)
		}
	}
}
