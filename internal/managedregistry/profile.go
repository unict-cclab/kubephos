package managedregistry

import (
	"regexp"
	"strings"
)

type Target struct {
	Project            string
	Upstream           string
	Type               string
	Source             string
	Repository         string
	Reference          string
	StaticRepositories []string
}

var proxyRepositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)
var proxyTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
var proxyDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ValidProxyImage reports whether an explicit public OCI image reference can be
// resolved through one of the pull-through caches installed on managed clusters.
func ValidProxyImage(value string) bool {
	if value == "" || len(value) > 512 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	slash := strings.IndexByte(value, '/')
	if slash <= 0 || !proxySource(value[:slash]) {
		return false
	}
	reference := value[slash+1:]
	if repository, digest, found := strings.Cut(reference, "@sha256:"); found {
		return proxyRepositoryPattern.MatchString(repository) && proxyDigestPattern.MatchString(digest)
	}
	colon := strings.LastIndexByte(reference, ':')
	if colon <= 0 {
		return false
	}
	return proxyRepositoryPattern.MatchString(reference[:colon]) && proxyTagPattern.MatchString(reference[colon+1:])
}

func ProxyImageVersion(value string) string {
	if _, digest, found := strings.Cut(value, "@sha256:"); found && len(digest) >= 12 {
		return digest[:12]
	}
	if colon := strings.LastIndexByte(value, ':'); colon >= 0 && colon+1 < len(value) {
		return value[colon+1:]
	}
	return "unknown"
}

func proxySource(value string) bool {
	for _, target := range Catalog() {
		if target.Source == value {
			return true
		}
	}

	return false
}
func Catalog() []Target {
	return []Target{
		{Project: "dockerhub-proxy", Upstream: "https://registry-1.docker.io", Type: "docker-hub", Source: "docker.io", Repository: "library/alpine", Reference: "3.23"},
		{Project: "k8s-proxy", Upstream: "https://registry.k8s.io", Type: "docker-registry", Source: "registry.k8s.io", Repository: "pause", Reference: "3.10.1"},
		{Project: "ghcr-proxy", Upstream: "https://ghcr.io", Type: "github-ghcr", Source: "ghcr.io", Repository: "chaos-mesh/chaos-daemon", Reference: "v2.8.3"},
		{Project: "gcr-proxy", Upstream: "https://gcr.io", Type: "docker-registry", Source: "gcr.io", Repository: "google-containers/pause", Reference: "3.2"},
		{Project: "quay-proxy", Upstream: "https://quay.io", Type: "docker-registry", Source: "quay.io", Repository: "prometheus/busybox", Reference: "latest"},
		{Project: "istio-proxy", Upstream: "https://registry.istio.io", Type: "docker-registry", Source: "registry.istio.io", Repository: "release/pilot", Reference: "1.30.0", StaticRepositories: []string{"release/pilot", "release/proxyv2"}},
	}
}
