package workflows

import (
	"encoding/json"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

func ResolveRuntimeTokens(stage domain.ResolvedPipelineStage, values map[string]string) (domain.ResolvedPipelineStage, error) {
	raw, err := json.Marshal(stage.Plan)
	if err != nil {
		return domain.ResolvedPipelineStage{}, err
	}
	value := string(raw)
	for token, replacement := range values {
		value = strings.ReplaceAll(value, token, replacement)
	}
	var plan domain.Plan
	if err := json.Unmarshal([]byte(value), &plan); err != nil {
		return domain.ResolvedPipelineStage{}, err
	}
	stage.Plan = plan
	return stage, nil
}
