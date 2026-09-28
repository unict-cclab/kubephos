package managedregistry

import "testing"

func TestValidProxyImage(t *testing.T) {
	valid := []string{
		"ghcr.io/unict-cclab/kube-scheduler:v0.1.0",
		"docker.io/library/alpine:3.23",
		"registry.k8s.io/pause@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	for _, image := range valid {
		if !ValidProxyImage(image) {
			t.Errorf("proxy image %q was rejected", image)
		}
	}

	invalid := []string{
		"example.com/team/scheduler:v1",
		"ghcr.io/team/scheduler",
		"ghcr.io/Team/scheduler:v1",
		"ghcr.io/team/scheduler:bad tag",
		"ghcr.io/team/scheduler@sha256:short",
	}
	for _, image := range invalid {
		if ValidProxyImage(image) {
			t.Errorf("invalid proxy image %q was accepted", image)
		}
	}
}

func TestProxyImageVersion(t *testing.T) {
	if value := ProxyImageVersion("ghcr.io/team/scheduler:v1.2.3"); value != "v1.2.3" {
		t.Fatalf("unexpected tag version %q", value)
	}
	if value := ProxyImageVersion("ghcr.io/team/scheduler@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); value != "aaaaaaaaaaaa" {
		t.Fatalf("unexpected digest version %q", value)
	}
}
