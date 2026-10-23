package podscheduling

import (
	_ "embed"
	"encoding/json"
)

const Name = "kubephos-pod-scheduling"

//go:embed observer.py
var Script string

type Target struct {
	Name     string            `json:"name"`
	Selector map[string]string `json:"selector"`
}

func Manifest(namespace, marker string, targets []Target) ([]byte, error) {
	values, err := json.Marshal(targets)
	if err != nil {
		return nil, err
	}
	meta := func() map[string]any {
		return map[string]any{"name": Name, "namespace": namespace, "annotations": map[string]string{"kubephos.dev/ownership-marker": marker}}
	}

	container := map[string]any{
		"name": "observer", "image": "locustio/locust:2.42.6", "command": []string{"python", "/observer/observer.py"},
		"env":            []any{map[string]any{"name": "WORKLOAD_TARGETS", "value": string(values)}, map[string]any{"name": "POD_NAMESPACE", "value": namespace}},
		"ports":          []any{map[string]any{"containerPort": 9647}},
		"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": 9647}, "periodSeconds": 1},
		"volumeMounts":   []any{map[string]any{"name": "observer", "mountPath": "/observer", "readOnly": true}},
	}
	pod := map[string]any{
		"serviceAccountName": Name, "nodeSelector": map[string]string{"kubephos.dev/role": "management"},
		"containers": []any{container}, "volumes": []any{map[string]any{"name": "observer", "configMap": map[string]any{"name": Name}}},
	}
	template := map[string]any{
		"metadata": map[string]any{"labels": map[string]string{"app.kubernetes.io/name": Name}, "annotations": map[string]string{"sidecar.istio.io/inject": "false", "prometheus.io/scrape": "true", "prometheus.io/path": "/stats/prometheus", "prometheus.io/port": "9647"}},
		"spec":     pod,
	}
	docs := []any{
		map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": meta()},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "Role", "metadata": meta(), "rules": []any{map[string]any{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get", "list", "watch"}}}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": meta(), "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": Name}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "name": Name, "namespace": namespace}}},
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta(), "data": map[string]string{"observer.py": Script}},
		map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": meta(), "spec": map[string]any{"replicas": 1, "selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": Name}}, "template": template}},
	}
	return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": docs})
}
