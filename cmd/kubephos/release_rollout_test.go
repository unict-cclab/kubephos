package main

import (
	"strings"
	"testing"
)

func TestManagedPreviousClusterLensImage(t *testing.T) {
	host := "harbor.example.test"
	prefix := host + "/kubephos-dev/cluster-lens:v1.1.0@sha256:"
	for _, version := range []string{"v1.1.0", "v1.2.0", "v1.2.1"} {
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
