package mesh

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/kubeconfig"
	"kubephos.dev/kubephos/internal/plugins"
)

const pluginID = "io.kubephos.cluster.cilium.mesh"
const artifactAPI = "artifacts.kubephos.dev/v1alpha1"

type Invocation struct {
	Input json.RawMessage `json:"input"`
}
type Member struct {
	Name             string `json:"name"`
	ClusterID        int    `json:"clusterId"`
	PodCIDR          string `json:"podCIDR"`
	ConnectionRef    string `json:"connectionRef"`
	ObservabilityRef string `json:"observabilityRef"`
	PlatformRef      string `json:"platformRef"`
	InventoryRef     string `json:"inventoryRef"`
}
type Spec struct {
	Name    string   `json:"name"`
	Members []Member `json:"members"`
}
type Connection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		PodCIDR    string `json:"podCIDR"`
		Networking string `json:"networking"`
		ClusterID  int    `json:"clusterId"`
		Server     string `json:"server"`
		Kubeconfig string `json:"kubeconfig"`
	} `json:"spec"`
}
type Runner interface {
	Run(context.Context, map[string]string, ...string) (string, error)
	Kubectl(context.Context, string, []byte, ...string) (string, error)
}
type Plugin struct{ Runner Runner }

var schema = json.RawMessage(`{"type":"object","additionalProperties":false,"required":["name","members"],"properties":{"name":{"type":"string","minLength":1,"maxLength":32},"members":{"type":"array","minItems":2,"maxItems":6,"items":{"type":"object","additionalProperties":false,"required":["name","clusterId","connectionRef","observabilityRef","platformRef","inventoryRef","podCIDR"],"properties":{"name":{"type":"string"},"clusterId":{"type":"integer","minimum":1,"maximum":255},"connectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"observabilityRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ObservabilityCapability","x-kubephos-artifact-version":"v1alpha1"},"platformRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ManagedPlatformCapability","x-kubephos-artifact-version":"v1alpha1"},"inventoryRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterInventory","x-kubephos-artifact-version":"v1alpha1"},"podCIDR":{"type":"string"}}}}}}`)

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{ID: pluginID, Name: "Cilium multi cluster environment", Version: "0.1.0", Description: "Connects independently provisioned Kubernetes clusters and verifies their Cilium mesh.", Schema: schema, ArtifactInputs: []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ClusterInventory", Version: "v1alpha1"}, {Type: "ObservabilityCapability", Version: "v1alpha1"}, {Type: "ManagedPlatformCapability", Version: "v1alpha1"}}, ArtifactOutputs: []domain.ArtifactContract{{Type: "ClusterEnvironment", Version: "v1alpha1"}}, Capabilities: []string{"cluster.mesh.connect", "cluster.mesh.preflight", "cluster.mesh.cleanup", "lifecycle.cleanup", "lifecycle.cleanup.compatible"}, Permissions: []string{"cluster.admin", "network.http"}}
}
func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	var spec Spec
	err := json.Unmarshal(invocation.Input, &spec)
	if err == nil {
		err = validateSpec(spec)
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "members", Message: err.Error()})
	}
	return report
}
func validateSpec(spec Spec) error {
	if spec.Name == "" || len(spec.Members) < 2 || len(spec.Members) > 6 {
		return errors.New("select two to six clusters")
	}
	names, ids, refs := map[string]bool{}, map[int]bool{}, map[string]bool{}
	networks := []netip.Prefix{}
	for _, member := range spec.Members {
		if member.Name == "" || names[member.Name] || member.ClusterID < 1 || member.ClusterID > 255 || ids[member.ClusterID] {
			return errors.New("cluster names and Cilium IDs must be unique")
		}
		network, err := netip.ParsePrefix(member.PodCIDR)
		if err != nil || !network.Addr().Is4() || network != network.Masked() {
			return errors.New("each mesh member requires an aligned IPv4 pod network")
		}
		for _, previous := range networks {
			if previous.Overlaps(network) {
				return errors.New("mesh pod networks overlap")
			}
		}
		networks = append(networks, network)
		names[member.Name] = true
		ids[member.ClusterID] = true
		for _, ref := range []string{member.ConnectionRef, member.ObservabilityRef, member.PlatformRef, member.InventoryRef} {
			if !strings.HasPrefix(ref, "art_") || refs[ref] {
				return errors.New("select distinct verified cluster artifacts")
			}
			refs[ref] = true
		}
	}
	return nil
}
func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	if err := validateSpec(spec); err != nil {
		return domain.Plan{}, err
	}
	inputs := []domain.ArtifactInput{}
	for i, member := range spec.Members {
		for _, input := range []struct{ name, kind, ref string }{{"connection", "ClusterConnection", member.ConnectionRef}, {"observability", "ObservabilityCapability", member.ObservabilityRef}, {"platform", "ManagedPlatformCapability", member.PlatformRef}, {"inventory", "ClusterInventory", member.InventoryRef}} {
			inputs = append(inputs, domain.ArtifactInput{Name: fmt.Sprintf("%s-%d", input.name, i), Type: input.kind, Version: "v1alpha1", ArtifactID: input.ref})
		}
	}
	outputs := []domain.ArtifactOutput{{Name: "cluster-environment", Type: "ClusterEnvironment", Version: "v1alpha1", MediaType: "application/json", Source: "/environment", Sensitive: true}}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{ID: "connect-clusters", Name: "Connect and verify Cilium mesh", Input: raw, Mutating: true, ArtifactInputs: inputs, Outputs: outputs}}}, ctx.Err()
}
func resolve(step domain.PlanStep) (Spec, map[string]string, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return spec, nil, err
	}
	if err := validateSpec(spec); err != nil {
		return spec, nil, err
	}
	configs := map[string]string{}
	for i, member := range spec.Members {
		raw, ok := step.ResolvedInputs[fmt.Sprintf("connection-%d", i)]
		if !ok || raw.ID != member.ConnectionRef || raw.Type != "ClusterConnection" {
			return spec, nil, errors.New("verified cluster connection unavailable")
		}
		var connection Connection
		if json.Unmarshal(raw.Value, &connection) != nil || connection.Kind != "ClusterConnection" || connection.Metadata.Name != member.Name || connection.Spec.Networking != "cilium" || connection.Spec.ClusterID != member.ClusterID || connection.Spec.PodCIDR != member.PodCIDR || connection.Spec.Kubeconfig == "" {
			return spec, nil, errors.New("cluster Cilium identity does not match the environment")
		}
		if err := kubeconfig.ValidateClientIdentity(connection.Spec.Kubeconfig, connection.Spec.Server); err != nil {
			return spec, nil, err
		}
		configs[member.Name] = connection.Spec.Kubeconfig

		for _, input := range []struct{ key, kind, ref string }{{"observability", "ObservabilityCapability", member.ObservabilityRef}, {"platform", "ManagedPlatformCapability", member.PlatformRef}, {"inventory", "ClusterInventory", member.InventoryRef}} {
			artifact, ok := step.ResolvedInputs[fmt.Sprintf("%s-%d", input.key, i)]
			if !ok || artifact.ID != input.ref || artifact.Type != input.kind {
				return spec, nil, errors.New("verified member capability unavailable")
			}
			var document struct {
				Kind     string `json:"kind"`
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Spec struct {
					ClusterServer string `json:"clusterServer"`
				} `json:"spec"`
			}
			if json.Unmarshal(artifact.Value, &document) != nil || document.Kind != input.kind || input.key == "inventory" && document.Metadata.Name != member.Name || input.key != "inventory" && document.Spec.ClusterServer != connection.Spec.Server {
				return spec, nil, errors.New("member capabilities belong to a different cluster")
			}
		}

	}
	return spec, configs, nil
}
func (p Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, configs, err := resolve(step)
	if err != nil {
		return health(err), nil
	}
	for _, config := range configs {
		if _, err := p.runner().Kubectl(ctx, config, nil, "get", "--raw=/readyz"); err != nil {
			return health(errors.New("member Kubernetes API is unavailable")), nil
		}
	}

	for _, member := range spec.Members {
		raw, err := p.runner().Kubectl(ctx, configs[member.Name], nil, "get", "configmap/cilium-config", "-n", "kube-system", "-o", "json")
		var config struct {
			Data map[string]string `json:"data"`
		}
		if err != nil || json.Unmarshal([]byte(raw), &config) != nil || config.Data["cluster-name"] != member.Name || config.Data["cluster-id"] != fmt.Sprint(member.ClusterID) {
			return health(errors.New("live Cilium identity does not match the environment")), nil
		}
	}
	return health(nil), nil
}
func (p Plugin) runner() Runner {
	if p.Runner != nil {
		return p.Runner
	}
	return &cliRunner{}
}
func (p Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, configs, err := resolve(step)
	if err != nil {
		return nil, err
	}
	runner := p.runner()
	if closer, ok := runner.(interface{ Close() }); ok {
		defer closer.Close()
	}
	for i := 0; i < len(spec.Members); i++ {
		for j := i + 1; j < len(spec.Members); j++ {
			source, target := spec.Members[i].Name, spec.Members[j].Name
			if err := log("info", "Connecting "+source+" and "+target); err != nil {
				return nil, err
			}
			if _, err := runner.Run(ctx, configs, "clustermesh", "connect", "--context", source, "--destination-context", target, "--allow-mismatching-ca"); err != nil {
				return nil, fmt.Errorf("connect cluster mesh %s to %s: %w", source, target, err)
			}
		}
	}
	if err := log("info", "Waiting for Cilium agents and ClusterMesh connections to become ready"); err != nil {
		return nil, err
	}
	if err := verifyMesh(ctx, runner, spec, configs); err != nil {
		return nil, err
	}
	if err := log("info", "Configuring cross-cluster network measurements and monitoring"); err != nil {
		return nil, err
	}
	if err := configureMeshMeasurements(ctx, runner, spec, configs); err != nil {
		return nil, err
	}
	envelope := func(kind string) map[string]any {
		return map[string]any{"apiVersion": artifactAPI, "kind": kind, "metadata": map[string]string{"name": spec.Name, "version": "v1alpha1"}, "spec": map[string]any{"mode": "multi", "networking": "cilium", "members": spec.Members}}
	}
	return json.Marshal(map[string]any{"environment": envelope("ClusterEnvironment")})
}
func verifyMesh(ctx context.Context, runner Runner, spec Spec, configs map[string]string) error {
	for _, member := range spec.Members {
		if _, err := runner.Run(ctx, configs, "clustermesh", "status", "--context", member.Name, "--wait", "--wait-duration", "10m"); err != nil {
			return fmt.Errorf("verify mesh on %s: %w", member.Name, err)
		}
	}
	return nil
}
func (p Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, configs, err := resolve(step)
	if err != nil {
		return health(err), nil
	}
	if step.Cleanup {
		return health(nil), nil
	}
	var value struct {
		Environment struct {
			Kind string `json:"kind"`
			Spec struct {
				Members []Member `json:"members"`
			} `json:"spec"`
		} `json:"environment"`
	}
	if json.Unmarshal(raw, &value) != nil || value.Environment.Kind != "ClusterEnvironment" || len(value.Environment.Spec.Members) != len(spec.Members) {
		return health(errors.New("invalid environment result")), nil
	}
	runner := p.runner()
	if closer, ok := runner.(interface{ Close() }); ok {
		defer closer.Close()
	}
	return health(verifyMesh(ctx, runner, spec, configs)), nil
}
func (p Plugin) Cleanup(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) error {
	spec, configs, err := resolve(step)
	if err != nil {
		return err
	}
	runner := p.runner()
	if closer, ok := runner.(interface{ Close() }); ok {
		defer closer.Close()
	}
	for i := 0; i < len(spec.Members); i++ {
		for j := i + 1; j < len(spec.Members); j++ {
			if _, err := runner.Run(ctx, configs, "clustermesh", "disconnect", "--context", spec.Members[i].Name, "--destination-context", spec.Members[j].Name); err != nil {
				return err
			}
		}
	}
	return nil
}
func health(err error) domain.HealthReport {
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error()}
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Cilium cluster mesh is ready"}
}
