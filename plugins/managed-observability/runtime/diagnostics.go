package managedobservability

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/plugins"
)

type containerState struct {
	Waiting *struct {
		Reason string `json:"reason"`
	} `json:"waiting"`
	Terminated *struct {
		Reason   string `json:"reason"`
		ExitCode int    `json:"exitCode"`
	} `json:"terminated"`
}
type containerStatus struct {
	Name                 string         `json:"name"`
	Ready                bool           `json:"ready"`
	RestartCount         int            `json:"restartCount"`
	State                containerState `json:"state"`
	LastTerminationState containerState `json:"lastState"`
}

func podFailureSummary(phase string, statuses []containerStatus) string {
	parts := []string{"phase=" + phase}
	for _, status := range statuses {
		if status.Ready {
			continue
		}
		if status.State.Waiting != nil {
			parts = append(parts, status.Name+": "+status.State.Waiting.Reason)
		}
		terminated := status.State.Terminated
		if terminated == nil {
			terminated = status.LastTerminationState.Terminated
		}
		if terminated != nil && terminated.ExitCode != 0 {
			parts = append(parts, fmt.Sprintf("%s: %s, exit code %d", status.Name, terminated.Reason, terminated.ExitCode))
		}
	}
	return strings.Join(parts, "; ")
}

// Capture bounded failure logs before cleanup removes the pod and its history.
func logFailedContainers(ctx context.Context, runner clusterRunner, kubeconfig string, log plugins.Logger) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-o", "json")
	if err != nil {
		_ = log("warning", "Could not read failed container diagnostics: "+err.Error())
		return
	}
	var pods podList
	if err = json.Unmarshal([]byte(raw), &pods); err != nil {
		_ = log("warning", "Could not decode failed container diagnostics.")
		return
	}
	remaining := 5
	for _, pod := range pods.Items {
		for _, status := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			if status.Ready || status.State.Terminated != nil && status.State.Terminated.ExitCode == 0 {
				continue
			}
			if remaining == 0 || ctx.Err() != nil {
				return
			}
			remaining--
			label := pod.Metadata.Name + "/" + status.Name
			_ = log("warning", "Container diagnostic "+label+": "+podFailureSummary(pod.Status.Phase, []containerStatus{status}))
			args := []string{"logs", pod.Metadata.Name, "-n", namespace, "-c", status.Name, "--tail=80", "--limit-bytes=8192"}
			if status.RestartCount > 0 {
				args = append(args, "--previous")
			}
			output, err := runner.Kubectl(ctx, kubeconfig, nil, args...)
			if err != nil {
				_ = log("warning", "Could not read container log "+label+": "+err.Error())
				continue
			}
			if output = strings.TrimSpace(output); output != "" {
				_ = log("warning", "Container log "+label+":\n"+output)
			}
		}
	}
}
