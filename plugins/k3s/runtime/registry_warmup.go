package k3s

import (
	"context"
	"fmt"
	"strings"

	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/plugins"
)

// Every node must prove an authenticated pull through the configured mirror.
// Larger probes and component checks run once on a management node. Kubernetes
// pulls workload images on other nodes when needed. Verify checks TLS, never pulls.
func warmRegistryProfile(ctx context.Context, runner commandRunner, machines machineSet, access machineAccess, profile registryProfile, spec Spec, log plugins.Logger) error {
	if err := log("info", "Checking registry access and a small mirrored image on every node"); err != nil {
		return err
	}
	commands := registryVerificationCommands(profile)
	for _, mirror := range profile.Endpoint.Spec.Mirrors {
		if mirror.Source == "docker.io" {
			commands = append(commands, registryPullCommand(mirror.Source+"/"+mirror.Probe))
			break
		}
	}
	if err := runAll(ctx, runner, machines.Spec.Machines, access, func(machine, int) string { return strings.Join(commands, " && ") }); err != nil {
		return fmt.Errorf("registry access check: %w", err)
	}
	target := machines.Spec.Machines[0]
	offset := spec.ControlPlanes
	for _, pool := range spec.NodePools {
		if pool.Role == "management" && pool.Count > 0 && offset < len(machines.Spec.Machines) {
			target = machines.Spec.Machines[offset]
			break
		}
		offset += pool.Count
	}

	if err := log("info", "Preparing management images on "+target.Name); err != nil {
		return err
	}
	for _, image := range registryWarmupImages(profile, spec) {
		if err := log("info", "Checking image "+image); err != nil {
			return err
		}
		if _, err := runner.Run(ctx, target, access, registryPullCommand(image)); err != nil {
			return fmt.Errorf("prepare management image %s on %s: %w", image, target.Name, err)
		}
	}
	return nil
}

func registryWarmupImages(profile registryProfile, spec Spec) []string {
	images := []string{}
	for _, mirror := range profile.Endpoint.Spec.Mirrors {
		if mirror.Source == "docker.io" || (spec.Networking == "cilium" && mirror.Source == "registry.istio.io") {
			continue
		}
		images = append(images, mirror.Source+"/"+mirror.Probe)
	}
	for _, component := range managedcomponents.Catalog() {
		images = append(images, managedcomponents.Image(profile.Endpoint.Spec.Host, component.Name, component.Version))
	}
	return images
}
