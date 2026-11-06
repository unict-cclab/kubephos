package applicationdeployer

import (
	"bytes"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"kubephos.dev/kubephos/internal/workloadresources"
	"strings"
)

func configureProxyResources(content string, workloads []workloadTarget, settings map[string]any) (string, error) {
	if len(settings) == 0 {
		return content, nil
	}
	ids := []string{}
	targets := map[string]string{}
	for _, w := range workloads {
		ids = append(ids, w.ID)
		targets[w.Kind+"/"+w.Name] = w.ID
	}
	if err := workloadresources.ValidateSettings(settings, ids); err != nil {
		return "", fmt.Errorf("Proxy resources: %w", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	defer encoder.Close()
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if len(resource) == 0 {
			continue
		}
		metadata, _ := resource["metadata"].(map[string]any)
		id, ok := targets[asString(resource["kind"])+"/"+asString(metadata["name"])]
		if ok {
			podSpec, _ := resource["spec"].(map[string]any)
			template, _ := podSpec["template"].(map[string]any)
			if template == nil {
				return "", fmt.Errorf("Pod template is unavailable for proxy resources on %s.", id)
			}
			meta := ensureMap(template, "metadata")
			annotations, _ := meta["annotations"].(map[string]any)
			labels, _ := meta["labels"].(map[string]any)
			disabled := fmt.Sprint(annotations["sidecar.istio.io/inject"]) == "false" || fmt.Sprint(labels["sidecar.istio.io/inject"]) == "false"
			if !disabled {
				values, err := workloadresources.ForService(settings, id)
				if err != nil {
					return "", err
				}
				for kind, raw := range values {
					resources, _ := raw.(map[string]any)
					for key, value := range resources {
						name := "sidecar.istio.io/proxy"
						if key == "cpu" {
							name += "CPU"
						} else {
							name += "Memory"
						}
						if kind == "limits" {
							name += "Limit"
						}
						ensureMap(meta, "annotations")[name] = value
					}
				}
			}
		}
		if err := encoder.Encode(resource); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

// Retain the same JSON resource contract used by application container settings.
func validateProxyResourceFields(values map[string]any) error {
	ids := []string{}
	if overrides, ok := values["resourceOverrides"].(map[string]any); ok {
		for id := range overrides {
			ids = append(ids, id)
		}
	}
	return workloadresources.ValidateSettings(values, ids)
}
