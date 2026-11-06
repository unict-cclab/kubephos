package applicationdeployer

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/placement"
)

// PlacementDefaults uses the same baseline placement transformation as deployment.
// The result is informational: it must never be submitted as custom settings.
func PlacementDefaults(content string, targets json.RawMessage) ([]placement.WorkloadDefaults, error) {
	var workloads []workloadTarget
	if err := json.Unmarshal(targets, &workloads); err != nil {
		return nil, err
	}
	manifest, err := assignApplicationPlacement(content, workloads)
	if err != nil {
		return nil, err
	}
	byResource := map[string]workloadTarget{}
	for _, w := range workloads {
		byResource[w.Kind+"/"+w.Name] = w
	}
	decoder := yaml.NewDecoder(strings.NewReader(manifest))
	result := []placement.WorkloadDefaults{}
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		meta, _ := resource["metadata"].(map[string]any)
		identity := asString(resource["kind"]) + "/" + asString(meta["name"])
		w, ok := byResource[identity]
		spec := applicationPodSpec(resource)
		if spec == nil {
			continue
		}
		if !ok {
			w.ID = identity
		}
		raw, err := json.Marshal(spec)
		if err != nil {
			return nil, err
		}
		var settings placement.WorkloadDefaults
		if err = json.Unmarshal(raw, &settings); err != nil {
			return nil, err
		}
		settings.ID = w.ID
		for _, trait := range w.Traits {
			if trait == trafficEntrypointTrait {
				settings.NodeBinding = "application-node"
			}
		}
		result = append(result, settings)
	}
	return result, nil
}
func asString(value any) string { text, _ := value.(string); return text }
