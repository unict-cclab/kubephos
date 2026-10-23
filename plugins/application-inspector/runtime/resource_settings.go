package applicationinspector

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/workloadresources"
)

// applyResourceSettings replaces package CPU and memory with explicit settings.
// Other resource types stay intact. Unconfigured containers, including init
// containers and auxiliary jobs, receive no CPU or memory defaults.
func applyResourceSettings(manifest []byte, descriptor catalog.Descriptor, values map[string]any) ([]byte, error) {
	services := []string{}
	targets := map[string]map[string]any{}
	for _, component := range descriptor.Spec.Interface.Components {
		if !contains(component.Traits, "scalable") && !contains(component.Traits, "schedulable") {
			targets[workloadIdentity(component.Workload)] = map[string]any{}
			continue
		}
		services = append(services, component.ID)
		resources, err := workloadresources.ForService(values, component.ID)
		if err != nil {
			return nil, err
		}
		targets[workloadIdentity(component.Workload)] = resources
	}
	if err := workloadresources.Validate(values, services); err != nil {
		return nil, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	result := []byte{}
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode application resources: %w", err)
		}
		if len(document) == 0 {
			continue
		}
		metadata, _ := document["metadata"].(map[string]any)
		key := fmt.Sprint(document["apiVersion"]) + "|" + fmt.Sprint(document["kind"]) + "|" + fmt.Sprint(metadata["name"])
		kind := fmt.Sprint(document["kind"])
		podWorkload := kind == "Pod" || kind == "CronJob" || kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet" || kind == "Job"
		if settings, exists := targets[key]; exists || podWorkload {
			path := []string{"spec", "template", "spec"}
			switch document["kind"] {
			case "Pod":
				path = []string{"spec"}
			case "CronJob":
				path = []string{"spec", "jobTemplate", "spec", "template", "spec"}
			case "Deployment", "StatefulSet", "DaemonSet", "Job":
			default:
				return nil, fmt.Errorf("resource settings do not support workload %s", key)
			}
			pod := document
			for _, field := range path {
				pod, _ = pod[field].(map[string]any)
			}
			containers, _ := pod["containers"].([]any)
			if len(containers) == 0 {
				return nil, fmt.Errorf("workload %s has no application containers", key)
			}
			initContainers, _ := pod["initContainers"].([]any)
			allContainers := append(append([]any{}, containers...), initContainers...)
			for index, item := range allContainers {
				containerSettings := settings
				if index >= len(containers) {
					containerSettings = nil
				}
				container, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("workload %s has an invalid container", key)
				}
				resources, _ := container["resources"].(map[string]any)
				if resources == nil {
					resources = map[string]any{}
				}
				for _, kind := range []string{"requests", "limits"} {
					if entries, ok := resources[kind].(map[string]any); ok {
						delete(entries, "cpu")
						delete(entries, "memory")
						if len(entries) == 0 {
							delete(resources, kind)
						}
					}
				}
				for kind, entries := range containerSettings {
					current, _ := resources[kind].(map[string]any)
					if current == nil {
						current = map[string]any{}
					}
					for resource, value := range entries.(map[string]any) {
						current[resource] = value
					}
					resources[kind] = current
				}
				// Validate the explicitly configured CPU and memory.
				effective := map[string]any{}
				for _, field := range []struct{ kind, suffix string }{{"requests", "Request"}, {"limits", "Limit"}} {
					entries, _ := resources[field.kind].(map[string]any)
					for _, resource := range []string{"cpu", "memory"} {
						if value, exists := entries[resource]; exists {
							effective[resource+field.suffix] = fmt.Sprint(value)
						}
					}
				}
				if _, err := workloadresources.Build(effective); err != nil {
					return nil, fmt.Errorf("resources for %s container %s: %w", metadata["name"], container["name"], err)
				}
				if len(resources) > 0 {
					container["resources"] = resources
				} else {
					delete(container, "resources")
				}
			}
			delete(targets, key)
		}
		encoded, err := yaml.Marshal(document)
		if err != nil {
			return nil, err
		}
		result = appendManifestDocument(result, encoded)
		if len(result) > maxManifestBytes {
			return nil, fmt.Errorf("application manifest exceeds %d bytes", maxManifestBytes)
		}
	}
	if len(targets) > 0 {
		return nil, errors.New("one or more resource override workloads are missing from the manifest")
	}
	return result, nil
}
