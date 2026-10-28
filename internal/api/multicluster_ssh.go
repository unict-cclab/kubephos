package api

import (
	"encoding/json"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
)

// The first member creates the key; all other members consume its verified,
// encrypted output. No key material is placed in configuration or plan inputs.
func withSharedMultiClusterSSHKey(definition domain.PipelineDefinition, members int) domain.PipelineDefinition {
	for index := range definition.Stages {
		stage := &definition.Stages[index]
		for member := 2; member <= members; member++ {
			if stage.ID != fmt.Sprintf("c%d-machines", member) {
				continue
			}
			var spec map[string]any
			_ = json.Unmarshal(stage.Spec, &spec)
			spec["machineAccessRef"] = ""
			stage.Spec = marshalRaw(spec)
			bindings := []domain.PipelineBinding{}
			for _, binding := range stage.Bindings {
				if binding.Path != "/machineAccessRef" {
					bindings = append(bindings, binding)
				}
			}
			stage.Bindings = append(bindings, domain.PipelineBinding{Path: "/machineAccessRef", FromStage: "c1-machines", FromOutput: "machine-access"})
		}
	}
	return definition
}
