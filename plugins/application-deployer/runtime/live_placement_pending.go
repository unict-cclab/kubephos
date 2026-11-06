package applicationdeployer

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"
)

func deleteWithPreconditions(ctx context.Context, runner commandRunner, kubeconfig, path string, metadata map[string]any) error {
	if asString(metadata["uid"]) == "" || asString(metadata["resourceVersion"]) == "" {
		return errors.New("The pending workload identity is unavailable.")
	}
	options, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": map[string]any{"uid": metadata["uid"], "resourceVersion": metadata["resourceVersion"]}})
	_, err := runner.Run(ctx, kubeconfig, options, "delete", "--raw", path, "-f", "-")
	return err
}

// Only pending pods belonging to this StatefulSet need manual replacement when
// an invalid template blocked its rolling update. Running pods keep the normal
// StatefulSet rollout semantics; no force deletion is used.
func restartPendingStatefulPods(ctx context.Context, runner commandRunner, kubeconfig, namespace string, obj map[string]any, selector map[string]string) error {
	metadata, _ := obj["metadata"].(map[string]any)
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(selector), "--field-selector=status.phase=Pending", "-o", "json")
	if err != nil {
		return err
	}
	var pods struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &pods) != nil {
		return errors.New("Pending application pods could not be read.")
	}
	for _, pod := range pods.Items {
		meta, _ := pod["metadata"].(map[string]any)
		owners, _ := meta["ownerReferences"].([]any)
		owned := false
		for _, rawOwner := range owners {
			owner, _ := rawOwner.(map[string]any)
			owned = owned || (owner["uid"] == metadata["uid"] && owner["kind"] == "StatefulSet")
		}
		if !owned || !podNeverStarted(pod) {
			continue
		}
		path := "/api/v1/namespaces/" + url.PathEscape(namespace) + "/pods/" + url.PathEscape(asString(meta["name"]))
		if err = deleteWithPreconditions(ctx, runner, kubeconfig, path, meta); err != nil {
			return err
		}
	}
	return nil
}
func podNeverStarted(pod map[string]any) bool {
	status, _ := pod["status"].(map[string]any)
	if status["phase"] != "Pending" {
		return false
	}
	for _, key := range []string{"containerStatuses", "initContainerStatuses"} {
		items, _ := status[key].([]any)
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			for _, stateKey := range []string{"state", "lastState"} {
				state, _ := item[stateKey].(map[string]any)
				if state["running"] != nil || state["terminated"] != nil {
					return false
				}
			}
		}
	}
	return true
}

func updatePendingJob(ctx context.Context, runner commandRunner, kubeconfig, namespace, name string, obj map[string]any, patch []map[string]any, controlName string) error {
	metadata, _ := obj["metadata"].(map[string]any)
	status, _ := obj["status"].(map[string]any)
	if metadata["deletionTimestamp"] != nil || positiveCount(status["succeeded"]) || positiveCount(status["failed"]) {
		return nil
	}
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", "batch.kubernetes.io/job-name="+name, "-o", "json")
	if err != nil {
		return err
	}
	var pods struct {
		Items []map[string]any `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &pods) != nil {
		return errors.New("Pending application jobs could not be read.")
	}
	for _, pod := range pods.Items {
		if !podNeverStarted(pod) {
			return nil
		}
	}
	if controlName == "" {
		return errors.New("Pending job placement requires a deployment retry.")
	}
	prepared, err := pendingJobReplacement(obj, patch, namespace)
	if err != nil {
		return err
	}
	value, _ := json.Marshal(prepared)
	// Replace only this never-started Job. An interrupted create is reported;
	// a missing Job is never automatically replayed from an old snapshot.
	path := "/apis/batch/v1/namespaces/" + url.PathEscape(namespace) + "/jobs/" + url.PathEscape(name)
	if err = deleteWithPreconditions(ctx, runner, kubeconfig, path, metadata); err != nil {
		return err
	}
	_, err = runner.Run(ctx, kubeconfig, value, "create", "-n", namespace, "-f", "-")
	return err
}
func pendingJobReplacement(obj map[string]any, patch []map[string]any, namespace string) (map[string]any, error) {
	raw, _ := json.Marshal(obj)
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	metadata := ensureMap(result, "metadata")
	for _, key := range []string{"uid", "resourceVersion", "generation", "creationTimestamp", "managedFields", "deletionTimestamp", "deletionGracePeriodSeconds"} {
		delete(metadata, key)
	}
	metadata["namespace"] = namespace
	metadata["annotations"] = patch[1]["value"]
	annotations, _ := metadata["annotations"].(map[string]any)
	delete(annotations, "kubectl.kubernetes.io/last-applied-configuration")
	delete(result, "status")
	pod := applicationPodSpec(result)
	pod["nodeSelector"] = patch[2]["value"]
	pod["tolerations"] = patch[3]["value"]
	spec := ensureMap(result, "spec")
	if spec["manualSelector"] != true {
		delete(spec, "selector")
		template := ensureMap(spec, "template")
		labels := ensureMap(ensureMap(template, "metadata"), "labels")
		for _, key := range []string{"controller-uid", "batch.kubernetes.io/controller-uid", "job-name", "batch.kubernetes.io/job-name"} {
			delete(labels, key)
		}
	}
	return result, nil
}

func positiveCount(value any) bool { number, ok := value.(float64); return ok && number > 0 }

func waitBootstrapCompletion(ctx context.Context, runner commandRunner, kubeconfig, namespace, name string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		output, err := runner.Run(ctx, kubeconfig, nil, "wait", "--for=condition=Complete", "job/"+name, "-n", namespace, "--timeout="+time.Until(deadline).Round(time.Millisecond).String())
		if err == nil {
			return nil
		}
		message := strings.ToLower(output + " " + err.Error())
		if !strings.Contains(message, "notfound") && !strings.Contains(message, "not found") && !strings.Contains(message, "deleted") {
			return err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
