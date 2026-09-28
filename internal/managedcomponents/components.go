package managedcomponents

import "strings"

const (
	Project            = "kubephos-dev"
	ClusterLensName    = "cluster-lens"
	ClusterLensVersion = "v1.2.6"
	MonAgentName       = "mon-agent"
	MonAgentVersion    = "v0.0.8"
	MentatName         = "mentat"
	MentatVersion      = "v0.3.5"
)

type Component struct {
	Name        string
	Version     string
	SourceImage string
}

func Catalog() []Component {
	return []Component{
		{Name: ClusterLensName, Version: ClusterLensVersion, SourceImage: "ghcr.io/unict-cclab/kubephos-cluster-lens:" + ClusterLensVersion},
		{Name: MonAgentName, Version: MonAgentVersion, SourceImage: "ghcr.io/unict-cclab/kubephos-mon-agent:" + MonAgentVersion},
		{Name: MentatName, Version: MentatVersion, SourceImage: "ghcr.io/unict-cclab/kubephos-mentat:" + MentatVersion},
	}
}

func Image(registryHost, name, version string) string {
	return strings.TrimSuffix(registryHost, "/") + "/" + Project + "/" + name + ":" + version
}
