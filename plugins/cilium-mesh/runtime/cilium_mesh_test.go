package mesh

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/domain"
)

func fixtureKubeconfig(name string) string {
	return fmt.Sprintf("apiVersion: v1\nkind: Config\ncurrent-context: %s\ncontexts:\n- name: %s\n  context: {cluster: %s, user: %s}\nclusters:\n- name: %s\n  cluster: {server: 'https://%s.test:6443'}\nusers:\n- name: %s\n  user: {client-certificate-data: Y2VydA==, client-key-data: a2V5}\n", name, name, name, name, name, name, name)
}
func fixtureStep(t *testing.T) domain.PlanStep {
	t.Helper()
	spec := Spec{Name: "research"}
	for i, name := range []string{"cluster-a", "cluster-b", "cluster-c"} {
		spec.Members = append(spec.Members, Member{Name: name, ClusterID: i + 1, PodCIDR: fmt.Sprintf("10.%d.0.0/16", 64+i), ConnectionRef: "art_connection_" + name, ObservabilityRef: "art_observability_" + name, PlatformRef: "art_platform_" + name, InventoryRef: "art_inventory_" + name})
	}
	raw, _ := json.Marshal(spec)
	plan, err := (Plugin{}).Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{}
	for i, member := range spec.Members {
		var connection Connection
		connection.Kind = "ClusterConnection"
		connection.Metadata.Name = member.Name
		connection.Spec.ClusterID = member.ClusterID
		connection.Spec.PodCIDR = member.PodCIDR
		connection.Spec.Networking = "cilium"
		connection.Spec.Server = "https://" + member.Name + ".test:6443"
		connection.Spec.Kubeconfig = fixtureKubeconfig(member.Name)
		value, _ := json.Marshal(connection)
		step.ResolvedInputs[fmt.Sprintf("connection-%d", i)] = domain.ResolvedArtifact{ID: member.ConnectionRef, Type: "ClusterConnection", Value: value}
		for _, input := range []struct{ key, kind, ref string }{{"observability", "ObservabilityCapability", member.ObservabilityRef}, {"platform", "ManagedPlatformCapability", member.PlatformRef}, {"inventory", "ClusterInventory", member.InventoryRef}} {
			value, _ := json.Marshal(map[string]any{"kind": input.kind, "metadata": map[string]string{"name": member.Name}, "spec": map[string]string{"clusterServer": connection.Spec.Server}})
			step.ResolvedInputs[fmt.Sprintf("%s-%d", input.key, i)] = domain.ResolvedArtifact{ID: input.ref, Type: input.kind, Value: value}
		}
	}
	return step
}

type meshTestRunner struct {
	commands [][]string
	applied  int
}

func (r *meshTestRunner) Run(_ context.Context, _ map[string]string, args ...string) (string, error) {
	r.commands = append(r.commands, append([]string{}, args...))
	return "connected", nil
}
func (r *meshTestRunner) Kubectl(_ context.Context, config string, stdin []byte, args ...string) (string, error) {
	if len(stdin) > 0 {
		r.applied++
	}
	command := strings.Join(args, " ")
	if strings.HasPrefix(command, "get prometheus") {
		return `{"items":[{"metadata":{"name":"prometheus"},"spec":{}}]}`, nil
	}
	if strings.HasPrefix(command, "get configmap/cilium-config") {
		for i, name := range []string{"cluster-a", "cluster-b", "cluster-c"} {
			if strings.Contains(config, "https://"+name+".test") {
				return fmt.Sprintf(`{"data":{"cluster-name":%q,"cluster-id":%q}}`, name, fmt.Sprint(i+1)), nil
			}
		}
	}
	return "ready", nil
}
func TestMeshConnectsEveryPairAndRetainsMembership(t *testing.T) {
	step := fixtureStep(t)
	runner := &meshTestRunner{}
	plugin := Plugin{Runner: runner}
	report, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || report.Status != domain.HealthHealthy {
		t.Fatalf("precheck: %+v %v", report, err)
	}
	raw, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	connections := 0
	statuses := 0
	for _, command := range runner.commands {
		if command[1] == "connect" {
			connections++
		}
		if command[1] == "status" {
			statuses++
		}
	}
	if connections != 3 || statuses != 3 || runner.applied < 6 {
		t.Fatalf("incomplete mesh: %d pairs, %d status checks, %d applied resources", connections, statuses, runner.applied)
	}
	if !strings.Contains(string(raw), `"mode":"multi"`) || !strings.Contains(string(raw), "art_connection_cluster-c") {
		t.Fatal("membership lost")
	}
}
func TestMeshRejectsCapabilitiesFromAnotherCluster(t *testing.T) {
	step := fixtureStep(t)
	value := step.ResolvedInputs["observability-0"]
	value.Value = []byte(`{"kind":"ObservabilityCapability","spec":{"clusterServer":"https://wrong.test:6443"}}`)
	step.ResolvedInputs["observability-0"] = value
	if _, _, err := resolve(step); err == nil {
		t.Fatal("accepted foreign cluster metrics")
	}
}
func TestMergedKubeconfigKeepsAllContexts(t *testing.T) {
	raw, err := MergedKubeconfig(map[string]string{"cluster-a": fixtureKubeconfig("cluster-a"), "cluster-b": fixtureKubeconfig("cluster-b")})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Contexts []any `yaml:"contexts"`
		Users    []any `yaml:"users"`
		Clusters []any `yaml:"clusters"`
	}
	if yaml.Unmarshal(raw, &result) != nil || len(result.Contexts) != 2 || len(result.Clusters) != 2 || len(result.Users) != 2 {
		t.Fatal("member contexts lost")
	}
}
