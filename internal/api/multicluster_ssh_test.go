package api

import (
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestMultiClusterSharedSSHBinding(t *testing.T) {
	definition := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "c1-machines", Spec: json.RawMessage(`{}`)}, {ID: "c2-machines", Spec: json.RawMessage(`{}`), Bindings: []domain.PipelineBinding{{Path: "/machineTemplateRef", FromStage: "template"}}}, {ID: "c3-machines", Spec: json.RawMessage(`{}`)}}}
	for i := 0; i < 2; i++ {
		definition = withSharedMultiClusterSSHKey(definition, 3)
	}
	if len(definition.Stages[0].Bindings) != 0 {
		t.Fatal("first cluster must generate its own key")
	}
	for _, stage := range definition.Stages[1:] {
		found := 0
		for _, binding := range stage.Bindings {
			if binding.Path == "/machineAccessRef" {
				found++
				if binding.FromStage != "c1-machines" || binding.FromOutput != "machine-access" {
					t.Fatal("wrong shared key source")
				}
			}
		}
		if found != 1 {
			t.Fatal("expected exactly one shared key binding")
		}
	}
	if definition.Stages[1].Bindings[0].Path != "/machineTemplateRef" {
		t.Fatal("template binding was lost")
	}
}
