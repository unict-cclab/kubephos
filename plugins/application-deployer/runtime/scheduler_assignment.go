package applicationdeployer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gopkg.in/yaml.v3"
)

func assignScheduler(value string, workloads []workloadTarget, schedulerName string, include, exclude []string) (string, error) {
	targets, err := selectedSchedulerTargets(workloads, include, exclude)
	if err != nil {
		return "", err
	}
	wanted := map[string]bool{}
	for _, target := range targets {
		wanted[target.Kind+"/"+target.Name] = true
	}
	decoder := yaml.NewDecoder(strings.NewReader(value))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	defer encoder.Close()
	matched := map[string]bool{}
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode scheduler workload manifest: %w", err)
		}
		if len(resource) == 0 {
			continue
		}
		metadata, _ := resource["metadata"].(map[string]any)
		key := fmt.Sprint(resource["kind"]) + "/" + fmt.Sprint(metadata["name"])
		if wanted[key] {
			podSpec, err := workloadPodSpec(resource)
			if err != nil {
				return "", fmt.Errorf("assign scheduler to %s: %w", key, err)
			}
			podSpec["schedulerName"] = schedulerName
			matched[key] = true
		}
		if err := encoder.Encode(resource); err != nil {
			return "", fmt.Errorf("encode scheduler workload manifest: %w", err)
		}
	}
	for key := range wanted {
		if !matched[key] {
			return "", fmt.Errorf("selected scheduler workload %s is absent from the runtime manifest", key)
		}
	}
	return output.String(), nil
}

func assignApplicationPlacement(value string, workloads []workloadTarget) (string, error) {
	wanted := map[string]bool{}
	for _, workload := range workloads {
		wanted[workload.Kind+"/"+workload.Name] = true
	}
	decoder := yaml.NewDecoder(strings.NewReader(value))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	defer encoder.Close()
	matched := map[string]bool{}
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("decode application workload manifest: %w", err)
		}
		if len(resource) == 0 {
			continue
		}
		metadata, _ := resource["metadata"].(map[string]any)
		key := fmt.Sprint(resource["kind"]) + "/" + fmt.Sprint(metadata["name"])
		if wanted[key] {
			podSpec, err := workloadPodSpec(resource)
			if err != nil {
				return "", fmt.Errorf("assign application placement to %s: %w", key, err)
			}
			nodeSelector, exists := podSpec["nodeSelector"].(map[string]any)
			if !exists {
				nodeSelector = map[string]any{}
				podSpec["nodeSelector"] = nodeSelector
			}
			if role, exists := nodeSelector["kubephos.dev/role"]; exists && fmt.Sprint(role) != "application" {
				return "", fmt.Errorf("application workload %s selects incompatible node role %s", key, role)
			}
			nodeSelector["kubephos.dev/role"] = "application"
			matched[key] = true
		}
		if err := encoder.Encode(resource); err != nil {
			return "", fmt.Errorf("encode application workload manifest: %w", err)
		}
	}
	for key := range wanted {
		if !matched[key] {
			return "", fmt.Errorf("application workload %s is absent from the runtime manifest", key)
		}
	}
	return output.String(), nil
}

func workloadPodSpec(resource map[string]any) (map[string]any, error) {
	spec, ok := resource["spec"].(map[string]any)
	if !ok {
		return nil, errors.New("workload spec is unavailable")
	}
	template, ok := spec["template"].(map[string]any)
	if !ok {
		return nil, errors.New("Pod template is unavailable")
	}
	podSpec, ok := template["spec"].(map[string]any)
	if !ok {
		return nil, errors.New("Pod spec is unavailable")
	}
	return podSpec, nil
}

func selectedSchedulerTargets(workloads []workloadTarget, include, exclude []string) ([]workloadTarget, error) {
	included := map[string]bool{}
	excluded := map[string]bool{}
	for _, id := range include {
		included[id] = true
	}
	for _, id := range exclude {
		excluded[id] = true
	}
	available := map[string]bool{}
	selected := []workloadTarget{}
	for _, workload := range workloads {
		if !containsString(workload.Traits, "schedulable") {
			continue
		}
		available[workload.ID] = true
		if (len(included) == 0 || included[workload.ID]) && !excluded[workload.ID] {
			selected = append(selected, workload)
		}
	}
	for id := range included {
		if !available[id] {
			return nil, fmt.Errorf("scheduler target %s is not a schedulable workload", id)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("scheduler selection does not contain workloads")
	}
	return selected, nil
}

func verifySchedulerAssignments(ctx context.Context, runner commandRunner, kubeconfig, namespace string, workloads []workloadTarget, schedulerName string, include, exclude []string) error {
	targets, err := selectedSchedulerTargets(workloads, include, exclude)
	if err != nil {
		return err
	}
	for _, target := range targets {
		value, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(target.Selector), "-o", "jsonpath={range .items[*]}{.spec.schedulerName}{'\\n'}{end}")
		if err != nil {
			return fmt.Errorf("read scheduler assignment for %s: %w", target.ID, err)
		}
		names := strings.Fields(value)
		if len(names) == 0 {
			return fmt.Errorf("workload %s has no Pods with a scheduler assignment", target.ID)
		}
		for _, name := range names {
			if name != schedulerName {
				return fmt.Errorf("workload %s uses scheduler %s instead of %s", target.ID, name, schedulerName)
			}
		}
	}
	return nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
