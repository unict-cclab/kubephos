package applicationdeployer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Report only unhealthy pods and their conditions, rather than repeatedly
// logging every ready replica until the plugin's log budget is exhausted.
func workloadReadinessSummary(raw string) (string, int, error) {
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Status struct {
				Phase                 string                                           `json:"phase"`
				Conditions            []struct{ Type, Status, Reason, Message string } `json:"conditions"`
				InitContainerStatuses []readinessContainerStatus                       `json:"initContainerStatuses"`
				ContainerStatuses     []readinessContainerStatus                       `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return "", 0, err
	}
	details := []string{}
	for _, pod := range list.Items {
		ready := false
		for _, condition := range pod.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
			}
		}
		if ready {
			continue
		}
		reasons := []string{}
		for _, condition := range pod.Status.Conditions {
			if condition.Status == "False" && condition.Reason != "" {
				reason := condition.Type + ": " + condition.Reason
				if condition.Message != "" {
					reason += " — " + diagnosticText(condition.Message)
				}
				reasons = append(reasons, reason)
			}
		}
		for _, container := range append(pod.Status.InitContainerStatuses, pod.Status.ContainerStatuses...) {
			if container.State.Waiting != nil {
				reason := container.Name + ": " + container.State.Waiting.Reason
				if container.State.Waiting.Message != "" {
					reason += " — " + diagnosticText(container.State.Waiting.Message)
				}
				reasons = append(reasons, reason)
			}
		}
		detail := pod.Metadata.Name + " [" + pod.Status.Phase + "]"
		if len(reasons) > 0 {
			detail += " " + strings.Join(reasons, "; ")
		}
		details = append(details, detail)
	}
	sort.Strings(details)
	unhealthy := len(details)
	summary := fmt.Sprintf("Workload readiness: %d/%d pods ready", len(list.Items)-unhealthy, len(list.Items))
	if unhealthy == 0 {
		return summary, 0, nil
	}
	const limit = 8
	if len(details) > limit {
		details = details[:limit]
	}
	summary += "\n" + strings.Join(details, "\n")
	if unhealthy > limit {
		summary += fmt.Sprintf("\n%d additional pods are not ready", unhealthy-limit)
	}
	return summary, unhealthy, nil
}

type readinessContainerStatus struct {
	Name  string `json:"name"`
	State struct {
		Waiting *struct{ Reason, Message string } `json:"waiting"`
	} `json:"state"`
}

func diagnosticText(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	const limit = 300
	characters := []rune(value)
	if len(characters) > limit {
		return string(characters[:limit]) + "…"
	}
	return value
}

// Keep the root error, but capture scheduling/image/readiness reasons before
// cleanup removes the namespace. A timed-out readiness wait needs its own
// short diagnostic budget; explicit cancellation does not start more work.
func workloadReadinessFailure(ctx context.Context, runner commandRunner, kubeconfig, namespace string, cause error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return cause
	}
	diagnosticContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	raw, err := runner.Run(diagnosticContext, kubeconfig, nil, "get", "pods", "-n", namespace, "-o", "json")
	if err != nil {
		return cause
	}
	summary, unhealthy, err := workloadReadinessSummary(raw)
	if err != nil || unhealthy == 0 {
		return cause
	}
	return fmt.Errorf("%w; %s", cause, summary)
}
