package mesh

import (
	"context"
	"encoding/json"
	"fmt"
)

func configureMeshMeasurements(ctx context.Context, runner Runner, spec Spec, configs map[string]string) error {
	clusters := []map[string]string{}
	for _, member := range spec.Members {
		clusters = append(clusters, map[string]string{"name": member.Name, "podCIDR": member.PodCIDR})
	}
	raw, _ := json.Marshal(clusters)
	service, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "mentat-global", "namespace": "kubephos-observability", "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos"}, "annotations": map[string]string{"service.cilium.io/global": "true", "service.cilium.io/global-sync-endpoint-slices": "true"}}, "spec": map[string]any{"clusterIP": "None", "selector": map[string]string{"app": "mentat"}, "ports": []map[string]any{{"name": "bandwidth", "port": 2113, "protocol": "UDP"}}}})
	for _, member := range spec.Members {
		config := configs[member.Name]
		if _, err := runner.Kubectl(ctx, config, service, "apply", "-f", "-"); err != nil {
			return fmt.Errorf("publish mesh measurement endpoints on %s: %w", member.Name, err)
		}
		patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []map[string]any{{"name": "mentat", "env": []map[string]string{{"name": "CLUSTER_NAME", "value": member.Name}, {"name": "MESH_CLUSTERS", "value": string(raw)}}}}}}}})
		if _, err := runner.Kubectl(ctx, config, nil, "patch", "daemonset/mentat", "-n", "kubephos-observability", "--type=strategic", "--patch", string(patch)); err != nil {
			return fmt.Errorf("configure Mentat on %s: %w", member.Name, err)
		}
		if _, err := runner.Kubectl(ctx, config, nil, "rollout", "status", "daemonset/mentat", "-n", "kubephos-observability", "--timeout=5m"); err != nil {
			return fmt.Errorf("wait for Mentat on %s: %w", member.Name, err)
		}
		lensPatch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []map[string]any{{"name": "cluster-lens", "env": []map[string]string{{"name": "CLUSTER_LENS_MESH_CLUSTERS", "value": string(raw)}, {"name": "CLUSTER_LENS_CLUSTER_NAME", "value": member.Name}}}}}}}})
		if _, err := runner.Kubectl(ctx, config, nil, "patch", "deployment/cluster-lens", "-n", "kubephos-observability", "--type=strategic", "--patch", string(lensPatch)); err != nil {
			return fmt.Errorf("configure Cluster Lens on %s: %w", member.Name, err)
		}
		if _, err := runner.Kubectl(ctx, config, nil, "rollout", "status", "deployment/cluster-lens", "-n", "kubephos-observability", "--timeout=5m"); err != nil {
			return fmt.Errorf("wait for Cluster Lens on %s: %w", member.Name, err)
		}
		if err := configureMeshFederation(ctx, runner, member.Name, configs); err != nil {
			return err
		}

	}
	return nil
}
