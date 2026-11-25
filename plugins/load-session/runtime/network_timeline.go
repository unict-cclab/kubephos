package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
)

const networkProfileLabel = "kubephos.dev/network-profile"
const networkClockFile = "/tmp/kubephos-network-start"

func networkClockName(marker string) string { return "kubephos-network-" + marker }

type timelinePod struct {
	Metadata struct {
		Name   string            `json:"name"`
		Labels map[string]string `json:"labels"`
	} `json:"metadata"`
	Status struct {
		Phase      string `json:"phase"`
		Containers []struct {
			Ready    bool `json:"ready"`
			Restarts int  `json:"restartCount"`
		} `json:"containerStatuses"`
	} `json:"status"`
}

func networkShapers(ctx context.Context, runner commandRunner, kubeconfig, marker string, duration int) ([]timelinePod, error) {
	selector := networkProfileLabel + "=" + marker + ",kubephos.dev/network-timeline=true"
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", "chaos-mesh", "-l", selector, "-o", "json")
	if err != nil {
		return nil, err
	}
	var value struct {
		Items []timelinePod `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &value) != nil || len(value.Items) == 0 {
		return nil, errors.New("The phased network profile has no running shapers.")
	}
	for _, pod := range value.Items {
		if pod.Metadata.Labels["kubephos.dev/network-duration"] != strconv.Itoa(duration) {
			return nil, errors.New("Network phase duration differs from the load profile.")
		}
		if pod.Status.Phase != "Running" || len(pod.Status.Containers) != 1 || !pod.Status.Containers[0].Ready || pod.Status.Containers[0].Restarts != 0 {
			return nil, fmt.Errorf("Network shaper %s is not healthy; the run cannot continue.", pod.Metadata.Name)
		}
	}
	return value.Items, nil
}

func parallelTimelinePods(ctx context.Context, pods []timelinePod, run func(context.Context, timelinePod) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	slots := make(chan struct{}, 4)
	failures := make(chan error, len(pods))
	for _, pod := range pods {
		group.Add(1)
		go func(pod timelinePod) {
			defer group.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				failures <- ctx.Err()
				return
			}
			defer func() { <-slots }()
			if err := run(ctx, pod); err != nil {
				failures <- err
				cancel()
			}
		}(pod)
	}
	group.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			return err
		}
	}
	return ctx.Err()
}

func networkTimelineResources(marker, namespace string) []map[string]any {
	name := networkClockName(marker)
	meta := map[string]any{"name": name, "annotations": map[string]string{applicationOwnershipKey: marker}}
	return []map[string]any{
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta, "data": map[string]string{"startUnix": ""}},
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": meta},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": meta, "rules": []any{map[string]any{"apiGroups": []string{""}, "resources": []string{"configmaps"}, "resourceNames": []string{name}, "verbs": []string{"get"}}}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": meta, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": name}, "subjects": []any{map[string]string{"kind": "ServiceAccount", "name": name, "namespace": namespace}}},
	}
}
