package applicationdeployer

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

func prepareNetworkPackage(ctx context.Context, runner commandRunner, cluster clusterConnection, content string, workloads []workloadTarget, endpoints []serviceEndpoint, pdb int) (string, []workloadTarget, []serviceEndpoint, int, error) {
	if cluster.Spec.Networking != "cilium" {
		manifest, targets, count, err := prepareRuntimePackage(ctx, runner, cluster.Spec.Kubeconfig, content, workloads, endpoints, pdb)
		return manifest, targets, endpoints, count, err
	}
	manifest, targets, converted, err := ciliumGatewayPackage(content, workloads, endpoints, pdb)
	return manifest, targets, converted, 0, err
}

type gatewayRule struct {
	path, service, rewrite string
	port                   int
	exact                  bool
}

var nginxLocation = regexp.MustCompile(`(?s)location\s+(?:(=)\s*)?([^\s{]+)\s*\{[^{}]*?proxy_pass\s+http://([a-zA-Z0-9.-]+)(?::([0-9]+))?([^;\s]*)\s*;`)

func ciliumGatewayPackage(content string, workloads []workloadTarget, endpoints []serviceEndpoint, pdb int) (string, []workloadTarget, []serviceEndpoint, error) {
	resources, err := decodeManifestResources(content)
	if err != nil {
		return "", nil, nil, err
	}
	proxies := trafficEntrypoints(workloads)
	proxyNames := map[string]bool{}
	configNames := map[string]bool{}
	for _, proxy := range proxies {
		proxyNames[proxy.Name] = true
		for _, resource := range resources {
			metadata, _ := resource["metadata"].(map[string]any)
			if resource["kind"] != proxy.Kind || metadata["name"] != proxy.Name {
				continue
			}
			spec, _ := resource["spec"].(map[string]any)
			template, _ := spec["template"].(map[string]any)
			pod, _ := template["spec"].(map[string]any)
			volumes, _ := pod["volumes"].([]any)
			for _, item := range volumes {
				volume, _ := item.(map[string]any)
				config, _ := volume["configMap"].(map[string]any)
				if name, ok := config["name"].(string); ok {
					configNames[name] = true
				}
			}
		}
	}
	rules := []gatewayRule{}
	for _, resource := range resources {
		metadata, _ := resource["metadata"].(map[string]any)
		if resource["kind"] != "ConfigMap" || !configNames[fmt.Sprint(metadata["name"])] {
			continue
		}
		data, _ := resource["data"].(map[string]any)
		for _, value := range data {
			text, _ := value.(string)
			for _, match := range nginxLocation.FindAllStringSubmatch(text, -1) {
				port := 80
				if match[4] != "" {
					port, _ = strconv.Atoi(match[4])
				}
				if !strings.HasPrefix(match[2], "/") {
					return "", nil, nil, fmt.Errorf("Cilium gateway requires explicit prefix or exact HTTP routes")
				}
				rules = append(rules, gatewayRule{path: match[2], service: match[3], port: port, rewrite: match[5], exact: match[1] == "="})
			}
		}
	}
	if len(proxies) > 0 && len(rules) == 0 {
		return "", nil, nil, fmt.Errorf("the application traffic entrypoint has no supported HTTP routes for Cilium")
	}
	proxyServices := map[string]bool{}
	for _, endpoint := range endpoints {
		if containsEntrypoint(proxies, endpoint.Component) {
			proxyServices[endpoint.Service] = true
		}
	}
	filtered := []map[string]any{}
	for _, resource := range resources {
		metadata, _ := resource["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		kind, _ := resource["kind"].(string)
		if proxyNames[name] && (kind == "DaemonSet" || kind == "Deployment" || kind == "Service") || kind == "Service" && proxyServices[name] || kind == "ConfigMap" && configNames[name] {
			continue
		}
		if kind == "Service" {
			annotations, _ := metadata["annotations"].(map[string]any)
			if annotations == nil {
				annotations = map[string]any{}
			}
			annotations["service.cilium.io/global"] = "true"
			annotations["service.cilium.io/affinity"] = "local"
			metadata["annotations"] = annotations
		}
		filtered = append(filtered, resource)
	}
	targets := []workloadTarget{}
	for _, workload := range workloads {
		if !hasTrait(workload, trafficEntrypointTrait) {
			targets = append(targets, workload)
		}
	}
	converted := append([]serviceEndpoint(nil), endpoints...)
	for i, endpoint := range converted {
		name := gatewayResourceName("entry-" + endpoint.ID)
		protocol := "HTTP"
		kind := "HTTPRoute"
		port := 80
		endpointRules := rules
		if endpoint.Protocol == "tcp" {
			protocol = "TCP"
			kind = "TCPRoute"
			port = endpoint.Port
			endpointRules = []gatewayRule{{service: endpoint.Service, port: endpoint.Port}}
		} else if !proxyNames[endpoint.Service] && !containsEntrypoint(proxies, endpoint.Component) {
			endpointRules = []gatewayRule{{path: "/", service: endpoint.Service, port: endpoint.Port}}
		}
		gateway := map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": "Gateway", "metadata": map[string]any{"name": name}, "spec": map[string]any{"gatewayClassName": "kubephos-cilium", "listeners": []any{map[string]any{"name": "application", "protocol": protocol, "port": port, "allowedRoutes": map[string]any{"namespaces": map[string]string{"from": "Same"}}}}}}
		routeRules := []any{}
		for _, rule := range endpointRules {
			value := map[string]any{"backendRefs": []any{map[string]any{"name": rule.service, "port": rule.port, "weight": 1}}}
			if protocol == "HTTP" {
				matchType := "PathPrefix"
				if rule.exact {
					matchType = "Exact"
				}
				value["matches"] = []any{map[string]any{"path": map[string]string{"type": matchType, "value": rule.path}}}
				if rule.rewrite != "" {
					replaceType, replaceField := "ReplacePrefixMatch", "replacePrefixMatch"
					if rule.exact {
						replaceType, replaceField = "ReplaceFullPath", "replaceFullPath"
					}
					value["filters"] = []any{map[string]any{"type": "URLRewrite", "urlRewrite": map[string]any{"path": map[string]string{"type": replaceType, replaceField: rule.rewrite}}}}
				}
			}
			routeRules = append(routeRules, value)
		}

		filtered = append(filtered, gateway)
		for start := 0; start < len(routeRules); start += 16 {
			end := start + 16
			if end > len(routeRules) {
				end = len(routeRules)
			}
			routeName := name
			if start > 0 {
				routeName = fmt.Sprintf("%s-%d", name, start/16+1)
			}
			route := map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": kind, "metadata": map[string]any{"name": routeName}, "spec": map[string]any{"parentRefs": []any{map[string]string{"name": name}}, "rules": routeRules[start:end]}}
			filtered = append(filtered, route)
		}

		converted[i].Service = "cilium-gateway-" + name
		converted[i].Port = port
	}
	manifest, err := encodeManifestResources(filtered)
	if err != nil {
		return "", nil, nil, err
	}
	manifest, err = assignApplicationPlacement(manifest, targets)
	if err != nil {
		return "", nil, nil, err
	}
	manifest, err = addPodDisruptionBudgets(manifest, targets, pdb)
	return manifest, targets, converted, err
}
func verifyCiliumGateways(ctx context.Context, runner commandRunner, cluster clusterConnection, namespace string, endpoints []serviceEndpoint) error {
	if cluster.Spec.Networking != "cilium" {
		return nil
	}
	for _, endpoint := range endpoints {
		name := strings.TrimPrefix(endpoint.Service, "cilium-gateway-")
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "wait", "--for=condition=Programmed", "gateway/"+name, "-n", namespace, "--timeout=5m"); err != nil {
			return fmt.Errorf("Cilium gateway %s is not programmed: %w", name, err)
		}
	}
	return nil
}
