package managedplatform

import (
	"context"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Istio serves both the istio-sidecar and istio-ambient components: one control plane,
// with the ambient node agents installed by the istio-ambient plugin when first used.
const (
	istioVersion     = "1.30.0"
	istioNamespace   = "istio-system"
	istioBaseRelease = "kubephos-istio-base"
	istiodRelease    = "kubephos-istiod"
)

var istiodChart = chartSpec{ID: "istiod", URL: "https://istio-release.storage.googleapis.com/charts/istiod-1.30.0.tgz", Digest: "7a34c7688da34107841e9585b226b94357769749f82502fdc4fb43a6bc3db320"}

var istioComponent = platformComponent{
	enabled:   selection.istio,
	namespace: istioNamespace,
	charts: []chartSpec{
		{ID: "istio-base", URL: "https://istio-release.storage.googleapis.com/charts/base-1.30.0.tgz", Digest: "aef833a87ed0022114c5759fe3ec6af7862c2a224d4c60e40ebaaef825e1e39d"},
		istiodChart,
	},
	releases: []releaseRef{{istioBaseRelease, istioNamespace}, {istiodRelease, istioNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		ambient := run.selected.has(managedcomponents.IstioAmbient)
		// A reconcile keeps ambient mode enabled while it stays selected.
		enabled := false
		if run.reconcile && ambient {
			enabled, _ = ambientReady(ctx, run.runner, run.kubeconfig)
		}
		if err := run.log("info", "Installing Istio base and control plane"); err != nil {
			return err
		}
		if err := run.helm(ctx, "istio-base", []byte("defaultRevision: default\n"), istioBaseRelease, istioNamespace, "10m"); err != nil {
			return fmt.Errorf("install Istio base: %w", err)
		}
		if err := placeManagementIngress(ctx, run.runner, run.kubeconfig); err != nil {
			return err
		}
		if err := run.helm(ctx, "istiod", istiodValues(enabled), istiodRelease, istioNamespace, "10m"); err != nil {
			return fmt.Errorf("install Istio control plane: %w", err)
		}
		if run.reconcile && !ambient {
			return removeAmbientAgents(ctx, run)
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		if err := run.rollout(ctx, "deployment/istiod", istioNamespace); err != nil {
			report := unhealthy("Istio control plane is not ready: "+err.Error(), "istio", "unhealthy")
			return &report, nil
		}
		run.checks["istio"] = istioVersion
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		if run.selected.cilium() {
			return nil
		}
		if err := removeAmbientAgents(ctx, run); err != nil {
			return err
		}
		for _, release := range []string{istiodRelease, istioBaseRelease} {
			if err := run.uninstall(ctx, release, istioNamespace); err != nil {
				return err
			}
		}
		return run.deleteNamespace(ctx, istioNamespace)
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("istio", istioVersion, istioNamespace)
		builder.version("istio." + istioVersion)
	},
}

func removeAmbientAgents(ctx context.Context, run *componentRun) error {
	for _, release := range []string{ztunnelRelease, istioCNIRelease} {
		if err := run.uninstall(ctx, release, istioNamespace); err != nil {
			return err
		}
	}
	return nil
}

// istiodValues configures the managed control plane. Ambient mode keeps the same
// settings and adds the ambient profile with the default image variant, the one
// the managed Harbor mirrors.
func istiodValues(ambient bool) []byte {
	values := "replicaCount: 1\nnodeSelector:\n  kubephos.dev/role: management\nmeshConfig:\n  enableTracing: true\n  extensionProviders:\n  - name: jaeger\n    opentelemetry:\n      service: jaeger.kubephos-observability.svc.cluster.local\n      port: 4317\n"
	if ambient {
		values += "profile: ambient\nglobal:\n  variant: \"\"\n"
	}
	return []byte(values)
}
