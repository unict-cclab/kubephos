package k3s

import (
	"context"
	"strings"
	"sync"
	"testing"
)

type registryRecorder struct {
	mu       sync.Mutex
	commands map[string][]string
}

func (r *registryRecorder) Run(_ context.Context, m machine, _ machineAccess, command string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.commands[m.Name] = append(r.commands[m.Name], command)
	return "", nil
}
func TestRegistryWarmupTargetsManagementAndVerifyDoesNotDownload(t *testing.T) {
	step := bootstrapStep(t, 1)
	spec, machines, access, err := resolve(step)
	if err != nil {
		t.Fatal(err)
	}
	spec.Networking = "cilium"
	spec.NodePools = []NodePool{{Name: "management", Role: "management", Count: 1}, {Name: "apps", Role: "application", Count: 1}}
	profile := registryProfile{}
	profile.Endpoint.Spec.Host = "registry.internal"
	profile.Endpoint.Spec.URL = "https://registry.internal"
	profile.Endpoint.Spec.Mirrors = legacyManagedMirrors(profile.Endpoint.Spec.URL)
	recorder := &registryRecorder{commands: map[string][]string{}}
	if err = warmRegistryProfile(context.Background(), recorder, machines, access, profile, spec, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for i, m := range machines.Spec.Machines {
		all := strings.Join(recorder.commands[m.Name], "\n")
		if !strings.Contains(all, "crictl pull 'docker.io/library/alpine:") {
			t.Fatalf("node %d did not test real mirrored pull", i)
		}
		if i != 1 && strings.Contains(all, "kubephos-dev/") {
			t.Fatal("management images downloaded to workload/control-plane node")
		}
		if strings.Contains(all, "registry.istio.io/") {
			t.Fatal("Cilium warmup pulled Istio")
		}
	}
	before := len(recorder.commands[machines.Spec.Machines[1].Name])
	if before < 4 {
		t.Fatal("management images not prepared")
	}
	light := strings.Join(registryVerificationCommands(profile), "\n")
	if strings.Contains(light, "crictl pull") || !strings.Contains(light, "--cacert") {
		t.Fatal("verify downloads images or omits TLS check")
	}
	spec.Networking = "flannel"
	if !strings.Contains(strings.Join(registryWarmupImages(profile, spec), "\n"), "registry.istio.io/") {
		t.Fatal("single-cluster profile lost Istio probe")
	}
}
