package applicationdeployer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"reflect"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/plugins"
)

const placementAnnotation = "kubephos.dev/applied-placement"

// A control object lives outside the application namespace so an edit can arrive
// before namespace creation. Only standalone deployments opt into this protocol.
type PlacementControl struct {
	ControlName string                         `json:"-"`
	Settings    *placement.ApplicationSettings `json:"settings"`
	Previous    *placement.ApplicationSettings `json:"previous"`
}

func readPlacementControl(ctx context.Context, runner commandRunner, kubeconfig, name string) (*PlacementControl, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "configmap", name, "-n", "default", "--ignore-not-found", "-o", "json")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var cm struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal([]byte(raw), &cm) != nil || cm.Metadata.Labels["kubephos.dev/placement-control"] != name {
		return nil, errors.New("Application placement control is invalid.")
	}
	var result PlacementControl
	if json.Unmarshal([]byte(cm.Data["placement"]), &result) != nil {
		return nil, errors.New("Application placement settings are invalid.")
	}
	if err := placement.ValidateApplication(result.Settings); err != nil {
		return nil, err
	}
	result.ControlName = name
	return &result, nil
}

func WritePlacementControl(ctx context.Context, kubeconfig, name string, control PlacementControl) error {
	if !validPlacementControlName(name) {
		return errors.New("Application placement control is invalid.")
	}
	// Refuse to adopt an unrelated object with the same name.
	if _, err := readPlacementControl(ctx, localRunner{}, kubeconfig, name); err != nil {
		return err
	}
	value, _ := json.Marshal(control)
	cm, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": "default", "labels": map[string]string{"kubephos.dev/placement-control": name, "app.kubernetes.io/managed-by": "kubephos"}}, "data": map[string]string{"placement": string(value)}})
	_, err := localRunner{}.Run(ctx, kubeconfig, cm, "apply", "-f", "-")
	return err
}
func validPlacementControlName(name string) bool {
	return len(name) <= 63 && regexp.MustCompile(`^kubephos-placement-[a-z0-9]+(-[a-z0-9]+)*$`).MatchString(name)
}

// ApplyLivePlacement touches declared workload templates only. Default selectors
// and tolerations are reconstructed from the generated application package.
func ApplyLivePlacement(ctx context.Context, kubeconfig, namespace, marker, content string, targets json.RawMessage, control PlacementControl) error {
	if control.ControlName != "" {
		latest, err := readPlacementControl(ctx, localRunner{}, kubeconfig, control.ControlName)
		if err != nil {
			return err
		}
		if latest != nil {
			control = *latest
		}
	}
	return applyLivePlacement(ctx, localRunner{}, kubeconfig, namespace, marker, content, targets, control)
}
func applyLivePlacement(ctx context.Context, runner commandRunner, kubeconfig, namespace, marker, content string, targets json.RawMessage, control PlacementControl) error {
	if err := placement.ValidateApplication(control.Settings); err != nil {
		return err
	}
	owned, err := namespaceOwnership(ctx, runner, kubeconfig, namespace, marker)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	defaults, err := PlacementDefaults(content, targets)
	if err != nil {
		return err
	}
	baseline := map[string]placement.WorkloadDefaults{}
	for _, d := range defaults {
		baseline[d.ID] = d
	}
	var workloads []workloadTarget
	if json.Unmarshal(targets, &workloads) != nil {
		return errors.New("Application workloads are invalid.")
	}
	docs, err := decodeManifestResources(content)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, w := range workloads {
		known[w.Kind+"/"+w.Name] = true
	}
	for _, obj := range docs {
		meta, _ := obj["metadata"].(map[string]any)
		name := asString(meta["name"])
		if obj["kind"] == "Job" && !known["Job/"+name] {
			workloads = append(workloads, workloadTarget{ID: "Job/" + name, Kind: "Job", Name: name})
		}
	}
	for _, w := range workloads {
		if w.Kind != "Deployment" && w.Kind != "StatefulSet" && w.Kind != "DaemonSet" && w.Kind != "Job" {
			continue
		}
		raw, err := runner.Run(ctx, kubeconfig, nil, "get", strings.ToLower(w.Kind)+"/"+w.Name, "-n", namespace, "--ignore-not-found", "-o", "json")
		if err != nil {
			return err
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}

		var obj map[string]any
		if json.Unmarshal([]byte(raw), &obj) != nil {
			return errors.New("Application workload could not be read.")
		}
		patch, err := livePlacementPatch(obj, w.ID, baseline[w.ID], control)
		if err != nil {
			return err
		}
		if patch == nil {
			continue
		}
		if w.Kind == "Job" {
			if err := updatePendingJob(ctx, runner, kubeconfig, namespace, w.Name, obj, patch, control.ControlName); err != nil {
				return err
			}
			continue
		}
		value, _ := json.Marshal(patch)
		if _, err = runner.Run(ctx, kubeconfig, nil, "patch", strings.ToLower(w.Kind)+"/"+w.Name, "-n", namespace, "--type=json", "-p", string(value)); err != nil {
			return fmt.Errorf("Could not update placement for %s: %w", w.ID, err)
		}
		if w.Kind == "StatefulSet" {
			if err := restartPendingStatefulPods(ctx, runner, kubeconfig, namespace, obj, w.Selector); err != nil {
				return err
			}
		}
	}
	return nil
}
func livePlacementPatch(obj map[string]any, id string, baseline placement.WorkloadDefaults, control PlacementControl) ([]map[string]any, error) {
	spec := applicationPodSpec(obj)
	if spec == nil {
		return nil, errors.New("Application pod template is unavailable.")
	}
	metadata, _ := obj["metadata"].(map[string]any)
	annotations, _ := metadata["annotations"].(map[string]any)
	previous := control.Previous
	if text, ok := annotations[placementAnnotation].(string); ok {
		var applied *placement.ApplicationSettings
		if json.Unmarshal([]byte(text), &applied) != nil {
			return nil, errors.New("Previous application placement is invalid.")
		}
		previous = applied
	}
	selector := map[string]string{}
	raw, _ := json.Marshal(spec["nodeSelector"])
	_ = json.Unmarshal(raw, &selector)
	if selector == nil {
		selector = map[string]string{}
	}
	oldSelector, oldTolerations := placement.Effective(previous, id)
	for key, value := range oldSelector {
		if _, managed := baseline.NodeSelector[key]; !managed && selector[key] == value {
			delete(selector, key)
		}
	}
	for key, value := range baseline.NodeSelector {
		selector[key] = value
	}
	nextSelector, nextTolerations := placement.Effective(control.Settings, id)
	for key, value := range nextSelector {
		if old, exists := baseline.NodeSelector[key]; exists && old != value {
			return nil, fmt.Errorf("%s requires %s=%s.", id, key, old)
		}
		selector[key] = value
	}
	tolerations := []any{}
	current, _ := spec["tolerations"].([]any)
	for _, item := range current {
		remove := false
		for _, t := range oldTolerations {
			if equivalentToleration(item, t) {
				remove = true
			}
		}
		for _, t := range baseline.Tolerations {
			if equivalentToleration(item, t) {
				remove = false
			}
		}
		if !remove {
			tolerations = append(tolerations, item)
		}
	}
	for _, t := range append(baseline.Tolerations, nextTolerations...) {
		found := false
		for _, item := range tolerations {
			found = found || equivalentToleration(item, t)
		}
		if !found {
			v, _ := json.Marshal(t)
			var item map[string]any
			_ = json.Unmarshal(v, &item)
			tolerations = append(tolerations, item)
		}
	}
	next, _ := json.Marshal(control.Settings)
	if string(next) == annotations[placementAnnotation] && reflect.DeepEqual(spec["nodeSelector"], toAny(selector)) && (reflect.DeepEqual(current, tolerations) || len(current) == 0 && len(tolerations) == 0) {
		return nil, nil
	}
	copyAnnotations := map[string]any{}
	for k, v := range annotations {
		copyAnnotations[k] = v
	}
	copyAnnotations[placementAnnotation] = string(next)
	return []map[string]any{{"op": "test", "path": "/metadata/resourceVersion", "value": metadata["resourceVersion"]}, {"op": "add", "path": "/metadata/annotations", "value": copyAnnotations}, {"op": "add", "path": "/spec/template/spec/nodeSelector", "value": selector}, {"op": "add", "path": "/spec/template/spec/tolerations", "value": tolerations}}, nil
}
func toAny(values map[string]string) map[string]any {
	result := map[string]any{}
	for k, v := range values {
		result[k] = v
	}
	return result
}

func followPlacement(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, content string, workloads []workloadTarget, log plugins.Logger) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	targets, _ := json.Marshal(workloads)
	go func() {
		defer close(done)
		ticker := time.NewTimer(0)
		defer ticker.Stop()
		lastError := ""
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				control, err := readPlacementControl(ctx, runner, kubeconfig, input.PlacementControlName)
				if err == nil && control != nil {
					err = applyLivePlacement(ctx, runner, kubeconfig, input.Namespace, input.Marker, content, targets, *control)
				}
				if err != nil && ctx.Err() == nil && err.Error() != lastError {
					_ = log("warning", "Application placement update is pending; it will be retried.")
					lastError = err.Error()
				}
				if err == nil {
					lastError = ""
				}
				ticker.Reset(2 * time.Second)
			}
		}
	}()
	return func() { cancel(); <-done }
}

func ValidatePlacement(content string, targets json.RawMessage, settings *placement.ApplicationSettings) error {
	var workloads []workloadTarget
	if err := json.Unmarshal(targets, &workloads); err != nil {
		return err
	}
	baseline, err := assignApplicationPlacement(content, workloads)
	if err != nil {
		return err
	}
	_, err = configureApplicationPlacement(baseline, workloads, settings)
	return err
}

func annotateAppliedPlacement(content string, settings *placement.ApplicationSettings) (string, error) {
	decoder := yaml.NewDecoder(strings.NewReader(content))
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	defer encoder.Close()
	value, _ := json.Marshal(settings)
	for {
		var obj map[string]any
		err := decoder.Decode(&obj)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if len(obj) == 0 {
			continue
		}
		if applicationPodSpec(obj) != nil {
			ensureMap(ensureMap(obj, "metadata"), "annotations")[placementAnnotation] = string(value)
		}
		if err = encoder.Encode(obj); err != nil {
			return "", err
		}
	}
	return out.String(), nil
}
