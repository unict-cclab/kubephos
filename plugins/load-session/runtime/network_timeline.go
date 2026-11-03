package loadsession

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
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

// Every shaper and load worker acknowledges the same future clock before
// traffic can begin. Slow or failed synchronization invalidates the run.
func armNetworkTimeline(ctx context.Context, runner commandRunner, kubeconfig string, deployment applicationDeployment, profile loadProfile, duration, replicas int) (time.Time, error) {
	pods, err := networkShapers(ctx, runner, kubeconfig, deployment.Metadata.OwnershipMarker, duration)
	if err != nil {
		return time.Time{}, err
	}
	lead := time.Duration(12+2*((len(pods)+replicas+3)/4)) * time.Second
	start := time.Now().UTC().Truncate(time.Second).Add(lead)
	syncCtx, cancel := context.WithDeadline(ctx, start.Add(-2*time.Second))
	defer cancel()
	timestamp := strconv.FormatInt(start.Unix(), 10)
	command := "now=$(date +%s); delta=$((" + timestamp + " - now)); if test \"$delta\" -lt 2; then echo 'network clock synchronization is late' >&2; exit 1; fi; printf '%s' " + timestamp + " > " + networkClockFile + ".tmp; mv " + networkClockFile + ".tmp " + networkClockFile + "; printf '%s\\n' \"$now\""
	if err := parallelTimelinePods(syncCtx, pods, func(ctx context.Context, pod timelinePod) error {
		before := time.Now().Unix()
		value, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", "chaos-mesh", pod.Metadata.Name, "-c", "node-shaper", "--", "/bin/sh", "-ec", command)
		if err != nil {
			return err
		}
		now, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil || now < before-1 || now > time.Now().Unix()+1 {
			return errors.New("Network node clock is not synchronized with KubePhos.")
		}
		return nil
	}); err != nil {
		return time.Time{}, fmt.Errorf("synchronize network shapers before load: %w", err)
	}
	clock := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": networkClockName(deployment.Metadata.OwnershipMarker), "annotations": map[string]string{applicationOwnershipKey: deployment.Metadata.OwnershipMarker}}, "data": map[string]string{"startUnix": timestamp}}
	raw, _ := json.Marshal(clock)
	if _, err := runner.Run(syncCtx, kubeconfig, raw, "apply", "-n", deployment.Spec.Namespace, "-f", "-"); err != nil {
		return time.Time{}, err
	}
	rawPods, err := runner.Run(syncCtx, kubeconfig, nil, "get", "pods", "-n", deployment.Spec.Namespace, "-l", selectorValue(profile.Workload.Selector), "-o", "json")
	if err != nil {
		return time.Time{}, err
	}
	var workers struct {
		Items []timelinePod `json:"items"`
	}
	if json.Unmarshal([]byte(rawPods), &workers) != nil || len(workers.Items) != replicas {
		return time.Time{}, errors.New("Load workers are not ready to acknowledge the network clock.")
	}
	err = parallelTimelinePods(syncCtx, workers.Items, func(ctx context.Context, pod timelinePod) error {
		before := time.Now().Unix()
		value, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", deployment.Spec.Namespace, pod.Metadata.Name, "-c", "locust", "--", "/bin/sh", "-ec", "while ! test -s /tmp/kubephos-network-clock-ready; do sleep 0.1; done; cat /tmp/kubephos-network-clock-ready; printf ' ' ; date +%s")
		if err != nil {
			return err
		}
		fields := strings.Fields(value)
		if len(fields) != 2 || fields[0] != timestamp {
			return errors.New("Load worker acknowledged a different network clock.")
		}
		now, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || now < before-1 || now > time.Now().Unix()+1 {
			return errors.New("Load worker clock is not synchronized with KubePhos.")
		}
		return nil
	})
	if err != nil {
		return time.Time{}, fmt.Errorf("synchronize load workers before traffic: %w", err)
	}
	if time.Until(start) < time.Second {
		return time.Time{}, errors.New("Network clock synchronization finished too late; no valid run can be collected.")
	}
	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-timer.C:
		return start, nil
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
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
