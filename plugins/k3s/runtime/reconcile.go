package k3s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/kubeconfig"
	"kubephos.dev/kubephos/internal/plugins"
)

func (p Plugin) reconcile(ctx context.Context, step domain.PlanStep, spec Spec, machines machineSet, access machineAccess, log plugins.Logger) (json.RawMessage, error) {
	value, ok := step.ResolvedInputs["previous-machines"]
	if !ok || value.ID != spec.PreviousMachineSetRef || value.Type != "MachineSet" {
		return nil, errors.New("verified previous topology is unavailable")
	}
	var previous machineSet
	if err := json.Unmarshal(value.Value, &previous); err != nil {
		return nil, err
	}
	if len(previous.Spec.Machines) == 0 || len(machines.Spec.Machines) == 0 || previous.Spec.Machines[0].ID != machines.Spec.Machines[0].ID || previous.Spec.Machines[0].Address != machines.Spec.Machines[0].Address {
		return nil, errors.New("the primary control plane cannot be replaced during maintenance")
	}
	protected := spec.ControlPlanes
	for _, pool := range spec.NodePools {
		if pool.Role == "management" {
			protected += pool.Count
		}
	}
	if len(previous.Spec.Machines) < protected || len(machines.Spec.Machines) < protected {
		return nil, errors.New("protected nodes are missing")
	}
	for i := 0; i < protected; i++ {
		a, b := previous.Spec.Machines[i], machines.Spec.Machines[i]
		if a.ID != b.ID || a.Name != b.Name || a.Address != b.Address {
			return nil, errors.New("control plane and management node identities are fixed")
		}
	}
	primary := machines.Spec.Machines[0]
	runner := p.runner()
	server := "https://" + net.JoinHostPort(primary.Address, "6443")
	inventory, err := readInventory(ctx, runner, primary, access, spec.ClusterName)
	if err != nil {
		return nil, err
	}
	current := map[string]bool{}
	for _, node := range inventory.Spec.Nodes {
		current[node.Name] = true
	}
	desired := map[string]bool{}
	known := map[string]bool{}
	for _, m := range machines.Spec.Machines {
		desired[m.Name] = true
		known[m.Name] = true
	}
	for _, m := range previous.Spec.Machines {
		known[m.Name] = true
	}
	for _, node := range inventory.Spec.Nodes {
		if (node.Role == "control-plane" || node.Pool == "management") && !desired[node.Name] {
			return nil, errors.New("control plane and management nodes are protected from removal")
		}
	}
	for name := range current {
		if !known[name] {
			return nil, fmt.Errorf("unmanaged Kubernetes node %s prevents reconciliation", name)
		}
	}
	versionText, err := runner.Run(ctx, primary, access, "sudo k3s --version")
	if err != nil {
		return nil, err
	}
	version := regexp.MustCompile(`v[0-9]+\.[0-9]+\.[0-9]+\+k3s[0-9]+`).FindString(versionText)
	if !validResultVersion(spec, version) {
		return nil, errors.New("the running K3s version must match the managed version before node maintenance")
	}
	token, err := runner.Run(ctx, primary, access, "sudo cat /var/lib/rancher/k3s/server/node-token")
	if err != nil {
		return nil, errors.New("could not read the cluster join token")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, errors.New("cluster join token is empty")
	}
	registry, err := resolveRegistry(step, spec)
	if err != nil {
		return nil, err
	}
	for index, m := range machines.Spec.Machines {
		if current[m.Name] {
			continue
		}
		if err := log("info", "Joining Kubernetes node "+m.Name); err != nil {
			return nil, err
		}
		if registry.Enabled {
			command, err := registryConfigurationCommand(registry)
			if err != nil {
				return nil, err
			}
			if _, err = runner.Run(ctx, m, access, command); err != nil {
				return nil, errors.New("could not configure the registry on " + m.Name)
			}
		}
		role := "agent"
		if index < spec.ControlPlanes {
			role = "server"
		}
		command := strings.ReplaceAll(installCommandForSpec(spec, role, m.Name, server, token, false, machineLabels(spec, index)), k3sVersion, version)
		if _, err := runner.Run(ctx, m, access, command); err != nil {
			return nil, fmt.Errorf("join %s: %s", m.Name, strings.ReplaceAll(err.Error(), token, "[redacted]"))
		}
	}
	// Wait for all replacement capacity before draining any existing machine.
	deadline := time.Now().Add(5 * time.Minute)
	for {
		inventory, err = readInventory(ctx, runner, primary, access, spec.ClusterName)
		ready := map[string]bool{}
		for _, n := range inventory.Spec.Nodes {
			ready[n.Name] = n.Ready
		}
		complete := err == nil
		for name := range desired {
			complete = complete && ready[name]
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			return nil, errors.New("desired nodes did not become ready before draining")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	pvJSON, err := runner.Run(ctx, primary, access, "sudo k3s kubectl get pv -o json")
	if err != nil {
		return nil, errors.New("persistent volumes could not be checked before removing nodes")
	}
	for index := len(previous.Spec.Machines) - 1; index >= 1; index-- {
		m := previous.Spec.Machines[index]
		if desired[m.Name] {
			continue
		}
		if err := validateRemovableVolumes([]byte(pvJSON), m.Name); err != nil {
			return nil, err
		}
		if current[m.Name] {
			if err := log("warning", "Draining Kubernetes node "+m.Name); err != nil {
				return nil, err
			}
			if _, err := runner.Run(ctx, primary, access, "sudo k3s kubectl drain "+shellQuote(m.Name)+" --ignore-daemonsets --delete-emptydir-data --timeout=5m"); err != nil {
				return nil, fmt.Errorf("drain %s failed; VM retained: %w", m.Name, err)
			}
		}
		// Stop the old kubelet before deleting its Node so it cannot register again.
		if _, err := runner.Run(ctx, m, access, "if test -x /usr/local/bin/k3s-uninstall.sh; then sudo /usr/local/bin/k3s-uninstall.sh; elif test -x /usr/local/bin/k3s-agent-uninstall.sh; then sudo /usr/local/bin/k3s-agent-uninstall.sh; fi"); err != nil {
			return nil, fmt.Errorf("uninstall %s failed; VM retained: %w", m.Name, err)
		}
		if _, err := runner.Run(ctx, primary, access, "sudo k3s kubectl delete node "+shellQuote(m.Name)+" --ignore-not-found"); err != nil {
			return nil, err
		}
		if err := waitForAPI(ctx, runner, primary, access, 2*time.Minute); err != nil {
			return nil, err
		}
	}
	inventory, err = waitForInventory(ctx, runner, primary, access, spec.ClusterName, len(machines.Spec.Machines), 5*time.Minute, version)
	if err != nil {
		return nil, err
	}
	if err := validateInventoryTopology(inventory, machines.Spec.Machines, spec); err != nil {
		return nil, err
	}
	if err := validateInventoryPools(inventory, spec); err != nil {
		return nil, err
	}
	if registry.Enabled {
		if err := verifyRegistryProfile(ctx, runner, machines, access, registry); err != nil {
			return nil, err
		}
	}
	config, err := runner.Run(ctx, primary, access, "sudo cat /etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, err
	}
	config = strings.ReplaceAll(config, "https://127.0.0.1:6443", server)
	config = strings.ReplaceAll(config, "https://localhost:6443", server)
	config, err = kubeconfig.WithIdentity(config, spec.ClusterName)
	if err != nil {
		return nil, err
	}
	if err := validateKubeconfig(config, server); err != nil {
		return nil, err
	}
	host := ""
	if registry.Enabled {
		host = registry.Endpoint.Spec.Host
	}
	inventory.Spec.Version = version
	return json.Marshal(clusterResult{ClusterConnection: clusterConnection{APIVersion: clusterAPIVersion, Kind: "ClusterConnection", Metadata: clusterConnectionMetadata{Name: spec.ClusterName, Version: version}, Spec: clusterConnectionSpec{PodCIDR: spec.PodCIDR, ServiceCIDR: spec.ServiceCIDR, Networking: spec.Networking, ClusterID: spec.ClusterID, Distribution: "k3s", Server: server, Kubeconfig: config, RegistryHost: host}}, ClusterInventory: inventory})
}

func validateRemovableVolumes(raw []byte, name string) error {
	var data struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Local        json.RawMessage `json:"local"`
				HostPath     json.RawMessage `json:"hostPath"`
				NodeAffinity struct {
					Required struct {
						NodeSelectorTerms []struct {
							MatchExpressions []struct {
								Key    string   `json:"key"`
								Values []string `json:"values"`
							} `json:"matchExpressions"`
						} `json:"nodeSelectorTerms"`
					} `json:"required"`
				} `json:"nodeAffinity"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return errors.New("persistent volume inventory is invalid")
	}
	for _, pv := range data.Items {
		if (len(pv.Spec.Local) == 0 || string(pv.Spec.Local) == "null") && (len(pv.Spec.HostPath) == 0 || string(pv.Spec.HostPath) == "null") {
			continue
		}
		matched, hasHostname := false, false
		for _, term := range pv.Spec.NodeAffinity.Required.NodeSelectorTerms {
			for _, expr := range term.MatchExpressions {
				if expr.Key == "kubernetes.io/hostname" {
					hasHostname = true
					for _, v := range expr.Values {
						matched = matched || v == name
					}
				}
			}
		}
		if matched || !hasHostname {
			return fmt.Errorf("local persistent volume %s prevents removal of %s; relocate its data first", pv.Metadata.Name, name)
		}
	}
	return nil
}
