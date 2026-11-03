package applicationdeployer

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Absent settings preserve existing manifests and the catalog's bootstrap opt-outs.
func configureIstioInjection(content string, workloads []workloadTarget, enabled *bool, overrides map[string]bool) (string, error) {
	if enabled == nil && len(overrides) == 0 {
		return content, nil
	}
	targets := map[string]bool{}
	known := map[string]bool{}
	for _, workload := range workloads {
		known[workload.ID] = true
		value, override := overrides[workload.ID]
		if !override {
			if enabled == nil {
				continue
			}
			value = *enabled
		}
		targets[workload.Kind+"/"+workload.Name] = value
	}
	for id := range overrides {
		if !known[id] {
			return "", fmt.Errorf("Istio override refers to unknown application service %s", id)
		}
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var result bytes.Buffer
	encoder := yaml.NewEncoder(&result)
	defer encoder.Close()
	found := map[string]bool{}
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
		key := fmt.Sprint(resource["kind"]) + "/" + fmt.Sprint(metadata["name"])
		value, wanted := targets[key]
		spec, _ := resource["spec"].(map[string]any)
		template, _ := spec["template"].(map[string]any)
		if !wanted && enabled != nil && !*enabled && template != nil {
			wanted = true
			value = false
		}
		if wanted {
			if template == nil {
				return "", fmt.Errorf("Pod template is unavailable for Istio service %s", key)
			}
			meta, _ := template["metadata"].(map[string]any)
			if meta == nil {
				meta = map[string]any{}
				template["metadata"] = meta
			}
			annotations, _ := meta["annotations"].(map[string]any)
			if annotations == nil {
				annotations = map[string]any{}
				meta["annotations"] = annotations
			}
			labels, _ := meta["labels"].(map[string]any)
			if labels == nil {
				labels = map[string]any{}
				meta["labels"] = labels
			}
			labels["sidecar.istio.io/inject"] = strconv.FormatBool(value)
			annotations["sidecar.istio.io/inject"] = strconv.FormatBool(value)
			found[key] = true
		}
		if err := encoder.Encode(resource); err != nil {
			return "", err
		}
	}
	for key := range targets {
		if !found[key] {
			return "", fmt.Errorf("Istio application workload %s is absent from the manifest", key)
		}
	}
	return result.String(), nil
}

func namespaceIstioInjection(input stepInput, cluster clusterConnection, endpoints []serviceEndpoint) string {
	if cluster.Spec.Networking == "cilium" {
		return "disabled"
	}
	enabled := !nativeTCPEndpoints(endpoints)
	if input.IstioInjection != nil {
		enabled = *input.IstioInjection
	}
	for _, value := range input.IstioInjectionOverrides {
		enabled = enabled || value
	}
	if enabled {
		return "enabled"
	}
	return "disabled"
}
