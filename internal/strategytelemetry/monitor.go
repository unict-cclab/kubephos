package strategytelemetry

// Resources gives an isolated strategy a scrape target and a token limited to monitoring URLs.
// Prometheus selects monitors carrying its Helm release label across experiment namespaces.
func Resources(namespace, component, bindingName, markerKey, marker string, selector map[string]string, port string) []map[string]any {
	name := component + "-metrics"
	metadata := func(resource string) map[string]any {
		return map[string]any{"name": resource, "namespace": namespace, "labels": map[string]string{"release": "kubephos-observability", "app.kubernetes.io/managed-by": "kubephos"}, "annotations": map[string]string{markerKey: marker}}
	}
	tokenMeta := metadata(name)
	tokenMeta["annotations"].(map[string]string)["kubernetes.io/service-account.name"] = name
	bindingMeta := metadata(bindingName + "-metrics")
	delete(bindingMeta, "namespace")
	return []map[string]any{
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": metadata(name)},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": bindingMeta, "subjects": []map[string]string{{"kind": "ServiceAccount", "name": name, "namespace": namespace}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "system:monitoring"}},
		{"apiVersion": "v1", "kind": "Secret", "metadata": tokenMeta, "type": "kubernetes.io/service-account-token"},
		{"apiVersion": "monitoring.coreos.com/v1", "kind": "PodMonitor", "metadata": metadata(name), "spec": map[string]any{"namespaceSelector": map[string]any{"matchNames": []string{namespace}}, "selector": map[string]any{"matchLabels": selector}, "podMetricsEndpoints": []map[string]any{{"port": port, "scheme": "https", "path": "/metrics", "interval": "5s", "scrapeTimeout": "4s", "tlsConfig": map[string]any{"insecureSkipVerify": true}, "authorization": map[string]any{"type": "Bearer", "credentials": map[string]string{"name": name, "key": "token"}}}}}},
	}
}
