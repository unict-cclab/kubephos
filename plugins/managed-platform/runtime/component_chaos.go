package managedplatform

import (
	"context"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Chaos Mesh runs the network fault injection between zones and its dashboard.
const (
	chaosVersion       = "2.8.3"
	chaosNamespace     = "chaos-mesh"
	chaosRelease       = "kubephos-chaos"
	chaosDashboardPort = 32300
)

var chaosValues = []byte("dnsServer:\n  nodeSelector:\n    kubephos.dev/role: management\nchaosDaemon:\n  runtime: containerd\n  socketPath: /run/k3s/containerd/containerd.sock\ncontrollerManager:\n  replicaCount: 1\n  nodeSelector:\n    kubephos.dev/role: management\ndashboard:\n  enabled: true\n  replicaCount: 1\n  nodeSelector:\n    kubephos.dev/role: management\n  securityMode: false\n  service:\n    type: NodePort\n    port: 2333\n    nodePort: 32300\n")

var chaosComponent = platformComponent{
	enabled:   func(s selection) bool { return s.has(managedcomponents.ChaosMesh) },
	namespace: chaosNamespace,
	charts:    []chartSpec{{ID: "chaos-mesh", URL: "https://charts.chaos-mesh.org/chaos-mesh-2.8.3.tgz", Digest: "c96a2d6490c1fbb0693e18b04e2ab6f60ca45046c64e98ca774f72ac1eaf2a47"}},
	releases:  []releaseRef{{chaosRelease, chaosNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		if err := run.log("info", "Installing the managed chaos runtime and dashboard"); err != nil {
			return err
		}
		if err := run.helm(ctx, "chaos-mesh", chaosValues, chaosRelease, chaosNamespace, "15m", "--wait-for-jobs"); err != nil {
			return fmt.Errorf("install chaos runtime: %w", err)
		}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		if err := run.rollout(ctx, "daemonset/chaos-daemon", chaosNamespace); err != nil {
			report := unhealthy("Chaos daemon is not ready on all nodes: "+err.Error(), "chaosDaemon", "unhealthy")
			return &report, nil
		}
		run.checks["chaos"] = chaosVersion
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		if err := run.uninstall(ctx, chaosRelease, chaosNamespace); err != nil {
			return err
		}
		return run.deleteNamespace(ctx, chaosNamespace)
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("chaos-mesh", chaosVersion, chaosNamespace)
		builder.endpoint("chaos-dashboard", chaosNamespace, "chaos-dashboard", chaosDashboardPort)
		builder.version("chaos." + chaosVersion)
	},
}
