package managedcomponents

import "strings"

const (
	Project            = "kubephos"
	LegacyProject      = "kubephos-dev"
	ClusterLensName    = "cluster-lens"
	ClusterLensVersion = "v1.3.2"
	MonAgentName       = "mon-agent"
	MonAgentVersion    = "v0.0.11"
	MentatName         = "mentat"
	MentatVersion      = "v0.3.9"
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

// Legacy artifacts omit the project; keep their original registry paths.
func ExistingProject(project string) string {
	if project == "" {
		return LegacyProject
	}
	return project
}
func ValidProject(project string) bool { return project == Project || project == LegacyProject }
func Image(registryHost, name, version string, projects ...string) string {
	project := Project
	if len(projects) > 0 {
		project = ExistingProject(projects[0])
	}
	return strings.TrimSuffix(registryHost, "/") + "/" + project + "/" + name + ":" + version
}
