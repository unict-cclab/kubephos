package k3s

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func ciliumSpec() Spec {
	return Spec{Networking: "cilium", ClusterName: "research-a", ClusterID: 1, PodCIDR: "10.64.0.0/16", ServiceCIDR: "10.128.0.0/16", ControlPlanes: 1, ControlPlaneZones: []string{"research-a"}, NodePools: []NodePool{{Name: "management", Role: "management", Count: 1, Zones: []string{"research-a"}}, {Name: "applications", Role: "application", Count: 2, Zones: []string{"research-a"}, Labels: map[string]string{"hardware": "gpu"}}}}
}
func TestCiliumBootstrapUsesUniqueNetworksAndDisablesDefaultCNI(t *testing.T) {
	spec := ciliumSpec()
	if err := validateNetworking(spec); err != nil {
		t.Fatal(err)
	}
	command := installCommandForSpec(spec, "server", "research-a-control-plane-1", "https://10.0.0.10:6443", "token", true, machineLabels(spec, 0))
	for _, required := range []string{"--flannel-backend=none", "--disable-kube-proxy", "--disable=traefik", "--cluster-cidr='10.64.0.0/16'", "--cluster-dns='10.128.0.10'"} {
		if !strings.Contains(command, required) {
			t.Fatalf("missing %s", required)
		}
	}
	if strings.Contains(command, "topology.kubernetes.io/zone") {
		t.Fatal("Cilium nodes still have zones")
	}
	worker := machineLabels(spec, 2)
	if worker["hardware"] != "gpu" || worker["kubephos.dev/cluster"] != "research-a" {
		t.Fatalf("pool labels lost: %+v", worker)
	}
	raw, err := ciliumBootstrapManifest(spec, "10.0.0.10")
	if err != nil {
		t.Fatal(err)
	}
	var chart struct {
		Spec struct {
			Values  string `yaml:"valuesContent"`
			Version string `yaml:"version"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &chart); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"gatewayAPI:", "enableEndpointSliceSynchronization: true", "kubeProxyReplacement: true", "id: 1", "name: research-a", "ipam:"} {
		if !strings.Contains(chart.Spec.Values, value) {
			t.Fatalf("missing Cilium value %s", value)
		}
	}
	var values struct {
		Mesh struct {
			Config struct {
				Enabled bool `yaml:"enabled"`
			} `yaml:"config"`
		} `yaml:"clustermesh"`
	}
	if err := yaml.Unmarshal([]byte(chart.Spec.Values), &values); err != nil {
		t.Fatal(err)
	}
	if !values.Mesh.Config.Enabled {
		t.Fatal("bootstrap must let Helm create ClusterMesh connection resources, including clustermesh-remote-users")
	}
	if chart.Spec.Version != ciliumVersion {
		t.Fatal("unversioned Cilium install")
	}
}
func TestCiliumRejectsInvalidNetworksAndReservedPoolLabels(t *testing.T) {
	for _, change := range []func(*Spec){func(s *Spec) { s.ClusterID = 0 }, func(s *Spec) { s.ServiceCIDR = s.PodCIDR }, func(s *Spec) { s.PodCIDR = "10.64.1.1/16" }, func(s *Spec) { s.ClusterName = "cluster-" }} {
		spec := ciliumSpec()
		change(&spec)
		if validateNetworking(spec) == nil {
			t.Fatalf("accepted %+v", spec)
		}
	}
	for _, key := range []string{"kubephos.dev/role", "topology.kubernetes.io/zone", "node-role.kubernetes.io/control-plane"} {
		if validPoolLabels(map[string]string{key: "worker"}) {
			t.Fatalf("accepted reserved %s", key)
		}
	}
}
