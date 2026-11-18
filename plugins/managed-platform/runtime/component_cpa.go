package managedplatform

import (
	"context"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// The Custom Pod Autoscaler operator runs the custom autoscaling strategies.
const (
	cpaVersion = "v1.4.2"
	cpaRelease = "kubephos-cpa-operator"
)

var cpaComponent = platformComponent{
	enabled:  func(s selection) bool { return s.has(managedcomponents.CPAOperator) },
	charts:   []chartSpec{{ID: "cpa-operator", URL: "https://github.com/jthomperoo/custom-pod-autoscaler-operator/releases/download/v1.4.2/custom-pod-autoscaler-operator-v1.4.2.tgz", Digest: "59f673d9a37f6667ba359bed3e1e8529b6f22470a2751d2279c7c1cd97e07b3c"}},
	releases: []releaseRef{{cpaRelease, systemNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		if err := run.log("info", "Installing the managed Custom Pod Autoscaler operator"); err != nil {
			return err
		}
		if err := run.helm(ctx, "cpa-operator", []byte("mode: cluster\n"), cpaRelease, systemNamespace, "10m"); err != nil {
			return fmt.Errorf("install Custom Pod Autoscaler operator: %w", err)
		}
		patch := []byte(`{"spec":{"template":{"spec":{"nodeSelector":{"kubephos.dev/role":"management"}}}}}`)
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, cpaPlacementArguments(patch)...); err != nil {
			return fmt.Errorf("place Custom Pod Autoscaler operator: %w", err)
		}
		if err := run.rollout(ctx, "deployment/custom-pod-autoscaler-operator", systemNamespace); err != nil {
			return fmt.Errorf("Custom Pod Autoscaler operator is not ready: %w", err)
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		if err := run.rollout(ctx, "deployment/custom-pod-autoscaler-operator", systemNamespace); err != nil {
			report := unhealthy("Custom Pod Autoscaler operator is not ready: "+err.Error(), "customPodAutoscalerOperator", "unhealthy")
			return &report, nil
		}
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "get", "customresourcedefinition", "custompodautoscalers.custompodautoscaler.com"); err != nil {
			report := unhealthy("Custom Pod Autoscaler CRD is unavailable", "customPodAutoscalerCRD", "unhealthy")
			return &report, nil
		}
		run.checks["customPodAutoscalerOperator"] = cpaVersion
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		return run.uninstall(ctx, cpaRelease, systemNamespace)
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("custom-pod-autoscaler-operator", cpaVersion, systemNamespace)
		builder.version("cpa." + cpaVersion)
	},
}

func cpaPlacementArguments(patch []byte) []string {
	return []string{"patch", "deployment", "custom-pod-autoscaler-operator", "-n", systemNamespace, "--type=merge", "--patch", string(patch)}
}
