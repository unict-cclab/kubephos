package applicationdeployer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/workloadresources"
)

func configureApplicationManifest(content string, workloads []workloadTarget, input stepInput) (string, error) {
	injection, overrides := input.IstioInjection, input.IstioInjectionOverrides
	if input.IstioDataPlane == istioAmbient {
		// Pods in an ambient namespace must not also get a sidecar.
		disabled := false
		injection, overrides = &disabled, nil
	}
	value, err := configureIstioInjection(content, workloads, injection, overrides)
	if err != nil {
		return "", err
	}
	value, err = configureProxyResources(value, workloads, input.IstioProxyResources)
	if err != nil {
		return "", err
	}
	if input.IstioDataPlane == istioAmbient {
		waypoint, err := workloadresources.ParseWaypoint(input.IstioWaypoint)
		if err != nil {
			return "", err
		}
		if value, err = configureWaypointServices(value, workloads, waypoint); err != nil {
			return "", err
		}
	}
	value, err = configureApplicationPlacement(value, workloads, input.ApplicationPlacement)
	if err == nil && input.PlacementControlName != "" {
		return annotateAppliedPlacement(value, input.ApplicationPlacement)
	}
	return value, err
}
func configureApplicationPlacement(content string, workloads []workloadTarget, settings *placement.ApplicationSettings) (string, error) {
	if settings == nil {
		return content, nil
	}
	if err := placement.ValidateApplication(settings); err != nil {
		return "", err
	}
	services := map[string]string{}
	known := map[string]bool{}
	for _, w := range workloads {
		services[w.Kind+"/"+w.Name] = w.ID
		known[w.ID] = true
	}
	for id := range settings.ServiceOverrides {
		if !known[id] {
			return "", fmt.Errorf("Placement override refers to unknown application service %s.", id)
		}
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	defer encoder.Close()
	for {
		var resource map[string]any
		if err := decoder.Decode(&resource); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return "", err
		}
		if len(resource) == 0 {
			continue
		}
		meta, _ := resource["metadata"].(map[string]any)
		identity := fmt.Sprint(resource["kind"]) + "/" + fmt.Sprint(meta["name"])
		spec := applicationPodSpec(resource)
		if spec != nil {
			selector, tolerations := placement.Effective(settings, services[identity])
			if len(selector) > 0 {
				current := ensureMap(spec, "nodeSelector")
				for key, value := range selector {
					if old, exists := current[key]; exists && fmt.Sprint(old) != value {
						return "", fmt.Errorf("Node selector %s for %s conflicts with its existing placement.", key, identity)
					}
					current[key] = value
				}
			}
			if len(tolerations) > 0 {
				current, _ := spec["tolerations"].([]any)
				for _, tol := range tolerations {
					raw, _ := json.Marshal(tol)
					var value map[string]any
					_ = json.Unmarshal(raw, &value)
					if _, ok := value["operator"]; !ok {
						value["operator"] = "Equal"
					}
					duplicate := false
					for _, existing := range current {
						if equivalentToleration(existing, value) {
							duplicate = true
							break
						}
					}
					if !duplicate {
						current = append(current, value)
					}
				}
				spec["tolerations"] = current
			}
		}
		if err := encoder.Encode(resource); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}
func applicationPodSpec(resource map[string]any) map[string]any {
	spec, _ := resource["spec"].(map[string]any)
	switch resource["kind"] {
	case "Pod":
		return spec
	case "CronJob":
		job, _ := spec["jobTemplate"].(map[string]any)
		jobSpec, _ := job["spec"].(map[string]any)
		template, _ := jobSpec["template"].(map[string]any)
		pod, _ := template["spec"].(map[string]any)
		return pod
	case "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "ReplicationController", "Job":
		template, _ := spec["template"].(map[string]any)
		pod, _ := template["spec"].(map[string]any)
		return pod
	default:
		return nil
	}
}
func equivalentToleration(a, b any) bool {
	normalize := func(value any) string {
		raw, _ := json.Marshal(value)
		var item map[string]any
		_ = json.Unmarshal(raw, &item)
		if item["operator"] == nil || item["operator"] == "" {
			item["operator"] = "Equal"
		}
		for _, key := range []string{"key", "value", "effect"} {
			if item[key] == nil {
				item[key] = ""
			}
		}
		raw, _ = json.Marshal(item)
		return string(raw)
	}
	return normalize(a) == normalize(b)
}
