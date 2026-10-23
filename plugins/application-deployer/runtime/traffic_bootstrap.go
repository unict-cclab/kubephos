package applicationdeployer

import (
	"context"
	"fmt"
	"strings"

	"kubephos.dev/kubephos/internal/plugins"
)

// Fixed ingress pods cannot move to another node when an experimental
// scheduler fills their target node. Assign them before application pods so
// their existing resource requests are accounted for during scheduling.
func applyRuntimeManifest(ctx context.Context, runner commandRunner, kubeconfig, namespace, content string, workloads []workloadTarget, log plugins.Logger) error {
	bootstrap, application, gateways, err := partitionTrafficBootstrap(content, workloads)
	if err != nil {
		return err
	}
	if len(gateways) == 0 {
		if _, err := runner.Run(ctx, kubeconfig, []byte(content), "apply", "--namespace", namespace, "-f", "-"); err != nil {
			return fmt.Errorf("apply application manifests: %w", err)
		}
		return nil
	}
	if err := log("info", fmt.Sprintf("Assigning %d fixed traffic proxies before scheduling application workloads", len(gateways))); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, kubeconfig, []byte(bootstrap), "apply", "--namespace", namespace, "-f", "-"); err != nil {
		return fmt.Errorf("apply traffic proxy prerequisites: %w", err)
	}
	for _, gateway := range gateways {
		// PodScheduled reserves capacity without depending on healthy upstream
		// services, which are deliberately created in the next phase.
		if _, err := waitSelectedPodsCondition(ctx, runner, kubeconfig, namespace, selectorValue(gateway.Selector), "PodScheduled"); err != nil {
			cause := fmt.Errorf("traffic proxy %s could not reserve capacity on node %s: %w", gateway.Name, gateway.NodeName, err)
			return workloadReadinessFailure(ctx, runner, kubeconfig, namespace, cause)
		}
	}
	if err := log("info", "Traffic proxies assigned; scheduling application workloads with ingress capacity already reserved"); err != nil {
		return err
	}
	if strings.TrimSpace(application) != "" {
		if _, err := runner.Run(ctx, kubeconfig, []byte(application), "apply", "--namespace", namespace, "-f", "-"); err != nil {
			return fmt.Errorf("apply application workloads: %w", err)
		}
	}
	return nil
}

func partitionTrafficBootstrap(content string, workloads []workloadTarget) (string, string, []workloadTarget, error) {
	gateways := []workloadTarget{}
	wanted := map[string]bool{}
	for _, workload := range workloads {
		if workload.NodeName != "" && hasTrait(workload, trafficEntrypointTrait) {
			gateways = append(gateways, workload)
			wanted[workload.Kind+"/"+workload.Name] = true
		}
	}
	if len(gateways) == 0 {
		return "", content, nil, nil
	}
	resources, err := decodeManifestResources(content)
	if err != nil {
		return "", "", nil, err
	}
	bootstrap, application := []map[string]any{}, []map[string]any{}
	found := map[string]bool{}
	for _, resource := range resources {
		metadata, _ := resource["metadata"].(map[string]any)
		kind := fmt.Sprint(resource["kind"])
		key := kind + "/" + fmt.Sprint(metadata["name"])
		if wanted[key] {
			found[key] = true
			bootstrap = append(bootstrap, resource)
			continue
		}
		switch kind {
		case "Pod", "Deployment", "StatefulSet", "DaemonSet", "ReplicaSet", "ReplicationController", "Job", "CronJob":
			application = append(application, resource)
		default:
			// Include DNS services, config maps, credentials and storage before
			// starting proxies; preserve their original declarations and ordering.
			bootstrap = append(bootstrap, resource)
		}
	}
	for key := range wanted {
		if !found[key] {
			return "", "", nil, fmt.Errorf("traffic proxy %s is absent from the runtime manifest", key)
		}
	}
	first, err := encodeManifestResources(bootstrap)
	if err != nil {
		return "", "", nil, err
	}
	second := ""
	if len(application) > 0 {
		second, err = encodeManifestResources(application)
		if err != nil {
			return "", "", nil, err
		}
	}
	return first, second, gateways, nil
}
