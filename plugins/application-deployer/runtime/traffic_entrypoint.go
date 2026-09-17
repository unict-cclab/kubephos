package applicationdeployer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	trafficEntrypointTrait = "traffic-entrypoint"
	trafficEntrypointLabel = "kubephos.dev/traffic-entrypoint"
	proxyInstanceLabel     = "kubephos.dev/proxy-instance"
)

var invalidDNSLabel = regexp.MustCompile(`[^a-z0-9-]+`)

type applicationNode struct {
	Name     string
	Hostname string
	Zone     string
}

type nodeList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Unschedulable bool `json:"unschedulable"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

func prepareRuntimePackage(ctx context.Context, runner commandRunner, kubeconfig, content string, workloads []workloadTarget, endpoints []serviceEndpoint) (string, []workloadTarget, int, error) {
	entrypoints := trafficEntrypoints(workloads)
	if len(entrypoints) == 0 {
		return content, workloads, 0, nil
	}
	nodes, err := discoverApplicationNodes(ctx, runner, kubeconfig)
	if err != nil {
		return "", nil, 0, err
	}
	manifest, effectiveWorkloads, err := expandTrafficEntrypoints(content, workloads, endpoints, nodes)
	if err != nil {
		return "", nil, 0, err
	}
	return manifest, effectiveWorkloads, len(nodes), nil
}

func discoverApplicationNodes(ctx context.Context, runner commandRunner, kubeconfig string) ([]applicationNode, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "nodes", "-l", "kubephos.dev/role=application", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("discover application nodes: %w", err)
	}
	var list nodeList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("decode application nodes: %w", err)
	}
	nodes := make([]applicationNode, 0, len(list.Items))
	for _, item := range list.Items {
		if item.Metadata.Name == "" || item.Spec.Unschedulable {
			return nil, fmt.Errorf("application node %q is unavailable for traffic ingress", item.Metadata.Name)
		}
		ready := false
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" {
				ready = condition.Status == "True"
			}
		}
		if !ready {
			return nil, fmt.Errorf("application node %s is not Ready", item.Metadata.Name)
		}
		hostname := item.Metadata.Labels["kubernetes.io/hostname"]
		if hostname == "" {
			return nil, fmt.Errorf("application node %s has no kubernetes.io/hostname label", item.Metadata.Name)
		}
		zone := item.Metadata.Labels["topology.kubernetes.io/zone"]
		if zone == "" {
			return nil, fmt.Errorf("application node %s has no topology.kubernetes.io/zone label", item.Metadata.Name)
		}
		nodes = append(nodes, applicationNode{Name: item.Metadata.Name, Hostname: hostname, Zone: zone})
	}
	if len(nodes) == 0 {
		return nil, errors.New("no Ready application nodes are available for traffic ingress")
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].Name < nodes[right].Name })
	return nodes, nil
}

func expandTrafficEntrypoints(content string, workloads []workloadTarget, endpoints []serviceEndpoint, nodes []applicationNode) (string, []workloadTarget, error) {
	if len(nodes) == 0 {
		return "", nil, errors.New("at least one application node is required")
	}
	resources, err := decodeManifestResources(content)
	if err != nil {
		return "", nil, err
	}
	entrypoints := trafficEntrypoints(workloads)
	if len(entrypoints) == 0 {
		return content, workloads, nil
	}
	endpointServices := map[string]string{}
	componentsWithEndpoint := map[string]bool{}
	for _, endpoint := range endpoints {
		endpointServices[endpoint.Service] = endpoint.Component
		componentsWithEndpoint[endpoint.Component] = true
	}
	for _, workload := range entrypoints {
		if !componentsWithEndpoint[workload.ID] {
			return "", nil, fmt.Errorf("traffic entrypoint %s has no declared service endpoint", workload.ID)
		}
	}
	generated := make([]map[string]any, 0, len(resources)+len(nodes)*len(entrypoints))
	replaced := map[string]bool{}
	for _, resource := range resources {
		kind, _ := resource["kind"].(string)
		metadata, _ := resource["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		matched := workloadTarget{}
		for _, workload := range entrypoints {
			if kind == workload.Kind && name == workload.Name {
				matched = workload
				break
			}
		}
		if matched.ID != "" {
			if matched.APIVersion != "apps/v1" || matched.Kind != "DaemonSet" {
				return "", nil, fmt.Errorf("traffic entrypoint %s must use an apps/v1 DaemonSet", matched.ID)
			}
			if replaced[matched.ID] {
				return "", nil, fmt.Errorf("traffic entrypoint %s is declared more than once", matched.ID)
			}
			replaced[matched.ID] = true
			for _, node := range nodes {
				deployment, err := trafficEntrypointDeployment(resource, matched, node, len(entrypoints))
				if err != nil {
					return "", nil, err
				}
				generated = append(generated, deployment)
			}
			continue
		}
		if kind == "Service" {
			if component, exists := endpointServices[name]; exists && containsEntrypoint(entrypoints, component) {
				spec, ok := resource["spec"].(map[string]any)
				if !ok {
					return "", nil, fmt.Errorf("traffic entrypoint service %s has no spec", name)
				}
				spec["selector"] = map[string]any{trafficEntrypointLabel: labelValue(component)}
			}
		}
		generated = append(generated, resource)
	}
	for _, workload := range entrypoints {
		if !replaced[workload.ID] {
			return "", nil, fmt.Errorf("traffic entrypoint workload %s/%s is missing from the manifest", workload.Kind, workload.Name)
		}
	}
	effective := make([]workloadTarget, 0, len(workloads)-len(entrypoints)+len(nodes)*len(entrypoints))
	for _, workload := range workloads {
		if !hasTrait(workload, trafficEntrypointTrait) {
			effective = append(effective, workload)
			continue
		}
		for _, node := range nodes {
			name := proxyDeploymentName(workload.Name, node.Name, len(entrypoints))
			effective = append(effective, workloadTarget{ID: name, APIVersion: "apps/v1", Kind: "Deployment", Name: name, Selector: map[string]string{proxyInstanceLabel: name}, Traits: append([]string(nil), workload.Traits...), NodeName: node.Name, NodeZone: node.Zone})
		}
	}
	encoded, err := encodeManifestResources(generated)
	if err != nil {
		return "", nil, err
	}
	return encoded, effective, nil
}

func trafficEntrypointDeployment(source map[string]any, workload workloadTarget, node applicationNode, entrypointCount int) (map[string]any, error) {
	spec, ok := source["spec"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("traffic entrypoint %s has no spec", workload.ID)
	}
	template, ok := spec["template"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("traffic entrypoint %s has no pod template", workload.ID)
	}
	copyTemplate, err := cloneMap(template)
	if err != nil {
		return nil, err
	}
	name := proxyDeploymentName(workload.Name, node.Name, entrypointCount)
	marker := labelValue(workload.ID)
	metadata, _ := source["metadata"].(map[string]any)
	deploymentMetadata, err := cloneMap(metadata)
	if err != nil {
		return nil, err
	}
	deploymentMetadata["name"] = name
	deploymentLabels := ensureMap(deploymentMetadata, "labels")
	deploymentLabels["app"] = name
	deploymentLabels["component"] = workload.ID
	deploymentLabels[trafficEntrypointLabel] = marker
	deploymentLabels[proxyInstanceLabel] = name
	templateMetadata := ensureMap(copyTemplate, "metadata")
	templateLabels := ensureMap(templateMetadata, "labels")
	templateLabels["app"] = name
	templateLabels["component"] = workload.ID
	templateLabels[trafficEntrypointLabel] = marker
	templateLabels[proxyInstanceLabel] = name
	templateLabels["topology.kubernetes.io/zone"] = node.Zone
	podSpec := ensureMap(copyTemplate, "spec")
	nodeSelector := ensureMap(podSpec, "nodeSelector")
	nodeSelector["kubernetes.io/hostname"] = node.Hostname
	return map[string]any{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   deploymentMetadata,
		"spec": map[string]any{
			"replicas": 1,
			"selector": map[string]any{"matchLabels": map[string]any{proxyInstanceLabel: name}},
			"template": copyTemplate,
		},
	}, nil
}

func trafficEntrypoints(workloads []workloadTarget) []workloadTarget {
	values := make([]workloadTarget, 0)
	for _, workload := range workloads {
		if hasTrait(workload, trafficEntrypointTrait) {
			values = append(values, workload)
		}
	}
	return values
}

func hasTrait(workload workloadTarget, trait string) bool {
	for _, current := range workload.Traits {
		if current == trait {
			return true
		}
	}
	return false
}

func containsEntrypoint(workloads []workloadTarget, id string) bool {
	for _, workload := range workloads {
		if workload.ID == id {
			return true
		}
	}
	return false
}

func proxyDeploymentName(workloadName, nodeName string, entrypointCount int) string {
	prefix := "gateway"
	if entrypointCount > 1 {
		prefix = dnsLabel(workloadName)
	}
	return boundedDNSLabel(prefix + "-" + dnsLabel(nodeName))
}

func dnsLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = invalidDNSLabel.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-")
	if value == "" {
		return "entrypoint"
	}
	return value
}

func boundedDNSLabel(value string) string {
	if len(value) <= 63 {
		return value
	}
	digest := sha256.Sum256([]byte(value))
	return strings.Trim(value[:50], "-") + "-" + hex.EncodeToString(digest[:])[:12]
}

func labelValue(value string) string {
	return boundedDNSLabel(dnsLabel(value))
}

func cloneMap(value map[string]any) (map[string]any, error) {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := yaml.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func ensureMap(parent map[string]any, key string) map[string]any {
	if value, ok := parent[key].(map[string]any); ok {
		return value
	}
	value := map[string]any{}
	parent[key] = value
	return value
}

func decodeManifestResources(content string) ([]map[string]any, error) {
	decoder := yaml.NewDecoder(strings.NewReader(content))
	resources := make([]map[string]any, 0)
	for {
		var resource map[string]any
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode traffic entrypoint manifest: %w", err)
		}
		if len(resource) > 0 {
			resources = append(resources, resource)
		}
	}
	return resources, nil
}

func encodeManifestResources(resources []map[string]any) (string, error) {
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	for _, resource := range resources {
		if err := encoder.Encode(resource); err != nil {
			return "", fmt.Errorf("encode runtime application manifest: %w", err)
		}
	}
	if err := encoder.Close(); err != nil {
		return "", err
	}
	return output.String(), nil
}
