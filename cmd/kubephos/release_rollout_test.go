package main

import (
	"strings"
	"testing"
)

func TestManagedComponentVersionsAreReleaseLocked(t *testing.T) {
	for _, value := range []struct{ name, version string }{{"cluster-lens", "v1.2.6"}, {"mon-agent", "v0.0.8"}, {"mentat", "v0.3.5"}} {
		if !managedComponentVersion(value.name, value.version) {
			t.Fatalf("managed component %s:%s was rejected", value.name, value.version)
		}
	}
	if managedComponentVersion("mon-agent", "latest") || managedComponentVersion("unknown", "v1.0.0") {
		t.Fatal("unmanaged component release was accepted")
	}
}

func TestManagedPreviousClusterLensImage(t *testing.T) {
	host := "harbor.example.test"
	prefix := host + "/kubephos-dev/cluster-lens:v1.1.0@sha256:"
	for _, version := range []string{"v1.1.0", "v1.2.0", "v1.2.1", "v1.2.2", "v1.2.3", "v1.2.4", "v1.2.5"} {
		if !managedPreviousClusterLensImage(host+"/kubephos-dev/cluster-lens:"+version, host) {
			t.Fatalf("valid managed previous tag rejected: %s", version)
		}
		image := host + "/kubephos-dev/cluster-lens:" + version + "@sha256:" + strings.Repeat("a", 64)
		if !managedPreviousClusterLensImage(image, host) {
			t.Fatalf("valid managed previous image rejected: %s", image)
		}
	}
	for _, image := range []string{
		prefix + strings.Repeat("a", 63),
		prefix + strings.Repeat("z", 64),
		"other.example.test/kubephos-dev/cluster-lens:v1.1.0@sha256:" + strings.Repeat("a", 64),
		host + "/kubephos-dev/cluster-lens:v1.0.0@sha256:" + strings.Repeat("a", 64),
	} {
		if managedPreviousClusterLensImage(image, host) {
			t.Fatalf("unexpected image accepted: %s", image)
		}
	}
}

func TestClusterRoleAllowsNodeMetrics(t *testing.T) {
	allowed := []byte(`{"rules":[{"apiGroups":["metrics.k8s.io"],"resources":["nodes"],"verbs":["get","list"]}]}`)
	if !clusterRoleAllowsNodeMetrics(allowed) {
		t.Fatal("expected node metrics access")
	}
	denied := []byte(`{"rules":[{"apiGroups":[""],"resources":["nodes"],"verbs":["get","list"]}]}`)
	if clusterRoleAllowsNodeMetrics(denied) {
		t.Fatal("core node access must not imply metrics access")
	}
}

func TestManagedRolloutDefinitions(t *testing.T) {
	monAgent, ok := managedRolloutDefinition("mon-agent")
	if !ok || monAgent.Resource != "deployment/kubephos-mon-agent" || monAgent.Container != "kubephos-mon-agent" {
		t.Fatalf("unexpected mon-agent rollout definition: %#v", monAgent)
	}
	mentat, ok := managedRolloutDefinition("mentat")
	if !ok || mentat.Resource != "daemonset/mentat" || mentat.Container != "mentat" {
		t.Fatalf("unexpected Mentat rollout definition: %#v", mentat)
	}
	if _, ok := managedRolloutDefinition("unknown"); ok {
		t.Fatal("unknown managed component was accepted")
	}
}

func TestManagedWorkloadImageAllowlist(t *testing.T) {
	spec, _ := managedRolloutDefinition("mon-agent")
	host := "harbor.example.test"
	target := host + "/kubephos-dev/mon-agent:v0.0.8@sha256:" + strings.Repeat("a", 64)
	for _, image := range []string{target, host + "/kubephos-dev/mon-agent:v0.0.8", "ghcr.io/unict-cclab/mon-agent:v0.0.7", host + "/kubephos-dev/mon-agent:v0.0.7"} {
		if !managedWorkloadImageAllowed(image, target, host, spec) {
			t.Fatalf("managed image rejected: %s", image)
		}
	}
	for _, image := range []string{host + "/kubephos-dev/mon-agent:latest", host + "/kubephos-dev/mon-agent:v0.0.8@sha256:" + strings.Repeat("z", 64), "docker.io/example/mon-agent:v0.0.8"} {
		if managedWorkloadImageAllowed(image, target, host, spec) {
			t.Fatalf("unexpected image accepted: %s", image)
		}
	}
}

func TestMentatPreviousManagedHarborImageIsAllowed(t *testing.T) {
	spec, _ := managedRolloutDefinition("mentat")
	host := "harbor.example.test"
	target := host + "/kubephos-dev/mentat:v0.3.5@sha256:" + strings.Repeat("a", 64)
	previous := host + "/kubephos-dev/mentat:v0.3.4@sha256:" + strings.Repeat("b", 64)
	if !managedWorkloadImageAllowed(previous, target, host, spec) {
		t.Fatalf("previous managed Mentat image rejected: %s", previous)
	}
}

func TestImageManifestDigestSelectsRuntimePlatform(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	raw := []byte(`{"schemaVersion":2,"manifests":[{"digest":"sha256:` + strings.Repeat("b", 64) + `","platform":{"os":"linux","architecture":"arm64"}},{"digest":"` + digest + `","platform":{"os":"linux","architecture":"amd64"}}]}`)
	selected, err := imageManifestDigest(raw, "linux", "amd64")
	if err != nil || selected != digest {
		t.Fatalf("unexpected selected digest %q: %v", selected, err)
	}
	if _, err := imageManifestDigest(raw, "linux", "s390x"); err == nil {
		t.Fatal("missing image platform was accepted")
	}
	single, err := imageManifestDigest([]byte(`{"schemaVersion":2,"config":{}}`), "linux", "amd64")
	if err != nil || !validImageDigest(single) || single == digest {
		t.Fatalf("unexpected single-manifest digest %q: %v", single, err)
	}
}
