package managedplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
)

func placeManagementIngress(ctx context.Context, runner clusterRunner, kubeconfig string) error {
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "helmchartconfig", "traefik", "-n", "kube-system", "--ignore-not-found", "-o", "json")
	if err != nil {
		return fmt.Errorf("read managed ingress configuration: %w", err)
	}
	manifest, err := managementIngressManifest([]byte(raw))
	if err != nil {
		return err
	}
	if _, err = runner.Kubectl(ctx, kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return fmt.Errorf("place managed ingress: %w", err)
	}
	return nil
}
func managementIngressManifest(raw []byte) ([]byte, error) {
	var current struct {
		Spec struct {
			ValuesContent string `json:"valuesContent"`
		} `json:"spec"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &current); err != nil {
			return nil, err
		}
	}
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(current.Spec.ValuesContent), &values); err != nil {
		return nil, err
	}
	if values == nil {
		values = map[string]any{}
	}
	selector, ok := values["nodeSelector"].(map[string]any)
	if !ok {
		selector = map[string]any{}
	}
	selector["kubephos.dev/role"] = "management"
	values["nodeSelector"] = selector
	content, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"apiVersion": "helm.cattle.io/v1", "kind": "HelmChartConfig", "metadata": map[string]string{"name": "traefik", "namespace": "kube-system"}, "spec": map[string]string{"valuesContent": string(content)}})
}
