package applicationdeployer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/workloadresources"
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
	if cluster.Spec.Networking == "cilium" || input.IstioDataPlane == istioAmbient {
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

// Ambient mode enrolls the whole namespace in ztunnel and sends its services
// through a single waypoint, which reports the L7 metrics sidecars would report.
const (
	istioAmbient    = "ambient"
	ambientWaypoint = "waypoint"
)

func validateIstioDataPlane(spec Spec) error {
	switch spec.IstioDataPlane {
	case "", "sidecar":
		if len(spec.IstioWaypoint) > 0 {
			return errors.New("Waypoint replicas and resources apply to Istio ambient mode only.")
		}
		return nil
	case istioAmbient:
		if (spec.IstioInjection != nil && *spec.IstioInjection) || len(spec.IstioInjectionOverrides) > 0 || len(spec.IstioProxyResources) > 0 {
			return errors.New("Istio ambient mode applies to the whole namespace: sidecar injection, service overrides and proxy resources must be left empty.")
		}
		_, err := workloadresources.ParseWaypoint(spec.IstioWaypoint)
		return err
	default:
		return errors.New("Istio data plane must be sidecar or ambient.")
	}
}

// ambientNamespaceLabels enrolls the namespace in ambient mode. Its services use the
// waypoint by default only when allServices is set; Service labels make exceptions.
func ambientNamespaceLabels(labels map[string]string, allServices bool) {
	labels["istio-injection"] = "disabled"
	labels["istio.io/dataplane-mode"] = istioAmbient
	if allServices {
		labels["istio.io/use-waypoint"] = ambientWaypoint
	}
}

const useWaypointLabel = "istio.io/use-waypoint"

// configureWaypointServices labels the Services of application components whose
// waypoint use differs from the namespace default: "waypoint" includes a service,
// "none" excludes it. A Service belongs to a component when its selector matches
// the labels of that component's pod template.
func configureWaypointServices(content string, workloads []workloadTarget, settings workloadresources.Waypoint) (string, error) {
	if len(settings.ServiceOverrides) == 0 {
		return content, nil
	}
	components := map[string]string{}
	for _, workload := range workloads {
		components[workload.Kind+"/"+workload.Name] = workload.ID
	}
	for id := range settings.ServiceOverrides {
		if !slices.ContainsFunc(workloads, func(workload workloadTarget) bool { return workload.ID == id }) {
			return "", fmt.Errorf("Waypoint override refers to unknown application service %s", id)
		}
	}
	decoder := yaml.NewDecoder(strings.NewReader(content))
	resources := []map[string]any{}
	podLabels := map[string]map[string]any{}
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
		resources = append(resources, resource)
		metadata, _ := resource["metadata"].(map[string]any)
		if id, ok := components[fmt.Sprint(resource["kind"])+"/"+fmt.Sprint(metadata["name"])]; ok {
			spec, _ := resource["spec"].(map[string]any)
			template, _ := spec["template"].(map[string]any)
			templateMetadata, _ := template["metadata"].(map[string]any)
			labels, _ := templateMetadata["labels"].(map[string]any)
			podLabels[id] = labels
		}
	}
	labelled := map[string]bool{}
	for _, resource := range resources {
		if resource["kind"] != "Service" {
			continue
		}
		spec, _ := resource["spec"].(map[string]any)
		selector, _ := spec["selector"].(map[string]any)
		if len(selector) == 0 {
			continue
		}
		for id, use := range settings.ServiceOverrides {
			if use == settings.AllServices || !selects(selector, podLabels[id]) {
				continue
			}
			metadata, _ := resource["metadata"].(map[string]any)
			labels, _ := metadata["labels"].(map[string]any)
			if labels == nil {
				labels = map[string]any{}
				metadata["labels"] = labels
			}
			value := "none"
			if use {
				value = ambientWaypoint
			}
			labels[useWaypointLabel] = value
			labelled[id] = true
		}
	}
	for id, use := range settings.ServiceOverrides {
		if use != settings.AllServices && !labelled[id] {
			return "", fmt.Errorf("Application service %s has no Kubernetes Service to route through the waypoint", id)
		}
	}
	var result bytes.Buffer
	encoder := yaml.NewEncoder(&result)
	for _, resource := range resources {
		if err := encoder.Encode(resource); err != nil {
			return "", err
		}
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return result.String(), nil
}

// selects reports whether every selector label matches the pod labels.
func selects(selector, labels map[string]any) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return len(labels) > 0
}

const waypointSettingsName = ambientWaypoint + "-settings"

// waypointManifest returns the namespace waypoint. When it is sized, a ConfigMap
// referenced through infrastructure.parametersRef overlays the Deployment Istio
// generates, so the replicas and proxy resources survive its reconciliation.
func waypointManifest(namespace, marker string, settings workloadresources.Waypoint) []byte {
	metadata := func(name string, labels map[string]string) map[string]any {
		return map[string]any{"name": name, "namespace": namespace, "annotations": map[string]string{ownershipKey: marker}, "labels": labels}
	}
	spec := map[string]any{"gatewayClassName": "istio-waypoint", "listeners": []map[string]any{{"name": "mesh", "port": 15008, "protocol": "HBONE"}}}
	gateway := map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway",
		"metadata": metadata(ambientWaypoint, map[string]string{"app.kubernetes.io/managed-by": "kubephos", "istio.io/waypoint-for": "service"}),
		"spec":     spec,
	}
	if settings.DefaultSize() {
		value, _ := json.Marshal(gateway)
		return value
	}
	overlay := map[string]any{}
	if settings.Replicas > 0 {
		overlay["replicas"] = settings.Replicas
	}
	if len(settings.Resources) > 0 {
		overlay["template"] = map[string]any{"spec": map[string]any{"containers": []map[string]any{{"name": "istio-proxy", "resources": settings.Resources}}}}
	}
	deployment, _ := json.Marshal(map[string]any{"spec": overlay})
	spec["infrastructure"] = map[string]any{"parametersRef": map[string]string{"group": "", "kind": "ConfigMap", "name": waypointSettingsName}}
	value, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": []map[string]any{
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": metadata(waypointSettingsName, map[string]string{"app.kubernetes.io/managed-by": "kubephos"}), "data": map[string]string{"deployment": string(deployment)}},
		gateway,
	}})
	return value
}

func waypointLogMessage(settings workloadresources.Waypoint) string {
	if settings.DefaultSize() {
		return "Starting the Istio ambient waypoint for the namespace"
	}
	replicas := max(settings.Replicas, 1)
	unit := "replicas"
	if replicas == 1 {
		unit = "replica"
	}
	return fmt.Sprintf("Starting the Istio ambient waypoint for the namespace with %d %s", replicas, unit)
}
