package managedcomponents

import (
	"fmt"
	"slices"
	"strings"
)

// Cluster component IDs selectable when a managed cluster is created.
const (
	Prometheus   = "prometheus"
	MonAgent     = MonAgentName
	Mentat       = MentatName
	ClusterLens  = ClusterLensName
	IstioSidecar = "istio-sidecar"
	IstioAmbient = "istio-ambient"
	Kiali        = "kiali"
	ChaosMesh    = "chaos-mesh"
	CPAOperator  = "cpa-operator"
	Logs         = "logs"
	Jaeger       = "jaeger"
	Headlamp     = "headlamp"
)

// ClusterComponent describes one component KubePhos can install on a managed cluster.
type ClusterComponent struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Core components are always installed and cannot be deselected.
	Core bool `json:"core"`
	// RequiresAny lists components of which at least one must also be selected.
	RequiresAny []string `json:"requiresAny,omitempty"`
	// UnsupportedNetworking lists cluster networking modes the component cannot run on.
	UnsupportedNetworking []string `json:"unsupportedNetworking,omitempty"`
	// Dashboards are the web consoles the component publishes on every node.
	Dashboards []Dashboard `json:"dashboards,omitempty"`
	// Registries are upstream image registries mirrored only for this component.
	Registries []string `json:"registries,omitempty"`
}

// Dashboard is a web console published as a NodePort.
type Dashboard struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	Path string `json:"path,omitempty"`
}

// clusterCatalog lists every component in installation order. Adding a component
// means adding it here and adding its installer in plugins/managed-platform/runtime.
var clusterCatalog = []ClusterComponent{
	{ID: Prometheus, Title: "Prometheus", Description: "Metrics collection and storage for experiments.", Core: true, Dashboards: []Dashboard{{Name: "Grafana", Port: 32000}, {Name: "Prometheus", Port: 32090}}},
	{ID: MonAgent, Title: "Monitoring agent", Description: "Node and workload monitoring agent.", Core: true},
	{ID: Mentat, Title: "Mentat", Description: "Managed measurement support service.", Core: true},
	{ID: ClusterLens, Title: "Cluster Lens", Description: "Inter-node latency and bandwidth measurements.", Core: true, Dashboards: []Dashboard{{Name: "Cluster Lens", Port: 32088}}},
	{ID: IstioSidecar, Title: "Istio (sidecar)", Description: "Istio control plane for sidecar-injected experiments.", UnsupportedNetworking: []string{"cilium"}, Registries: []string{"registry.istio.io"}},
	{ID: IstioAmbient, Title: "Istio (ambient)", Description: "Istio ambient mode with ztunnel and waypoints.", UnsupportedNetworking: []string{"cilium"}, Registries: []string{"registry.istio.io"}},
	{ID: Kiali, Title: "Kiali", Description: "Service mesh console.", RequiresAny: []string{IstioSidecar, IstioAmbient}, UnsupportedNetworking: []string{"cilium"}, Dashboards: []Dashboard{{Name: "Kiali", Port: 30001, Path: "/kiali"}}},
	{ID: ChaosMesh, Title: "Chaos Mesh", Description: "Network fault injection between zones.", Dashboards: []Dashboard{{Name: "Chaos dashboard", Port: 32300}}},
	{ID: CPAOperator, Title: "Custom Pod Autoscaler operator", Description: "Runtime for custom autoscaling strategies."},
	{ID: Logs, Title: "Logs (Loki and Alloy)", Description: "Log collection and runtime pipeline logs.", Dashboards: []Dashboard{{Name: "Loki API", Port: 32099}}},
	{ID: Jaeger, Title: "Jaeger", Description: "Distributed tracing.", Dashboards: []Dashboard{{Name: "Jaeger", Port: 30002}}},
	{ID: Headlamp, Title: "Headlamp", Description: "Kubernetes web console.", Dashboards: []Dashboard{{Name: "Headlamp", Port: 30003}}},
}

// ClusterCatalog returns every selectable cluster component in installation order.
func ClusterCatalog() []ClusterComponent {
	values := make([]ClusterComponent, len(clusterCatalog))
	for index, component := range clusterCatalog {
		component.RequiresAny = slices.Clone(component.RequiresAny)
		component.UnsupportedNetworking = slices.Clone(component.UnsupportedNetworking)
		component.Dashboards = slices.Clone(component.Dashboards)
		component.Registries = slices.Clone(component.Registries)
		values[index] = component
	}
	return values
}

// DefaultClusterComponents returns the full set supported by the networking mode.
// Clusters saved before component selection existed are treated as having this set.
func DefaultClusterComponents(networking string) []string {
	values := []string{}
	for _, component := range clusterCatalog {
		if !slices.Contains(component.UnsupportedNetworking, networking) {
			values = append(values, component.ID)
		}
	}
	return values
}

// ResolveClusterComponents validates a requested selection, adds the core components and
// returns the set in catalog order. A nil request selects the defaults for the networking mode.
func ResolveClusterComponents(requested []string, networking string) ([]string, error) {
	if requested == nil {
		return DefaultClusterComponents(networking), nil
	}
	selected := map[string]bool{}
	for _, value := range requested {
		id := strings.TrimSpace(value)
		index := slices.IndexFunc(clusterCatalog, func(component ClusterComponent) bool { return component.ID == id })
		if index < 0 {
			return nil, fmt.Errorf("Unknown cluster component %q.", id)
		}
		if slices.Contains(clusterCatalog[index].UnsupportedNetworking, networking) {
			return nil, fmt.Errorf("%s is not available with %s networking.", clusterCatalog[index].Title, networking)
		}
		selected[id] = true
	}
	resolved := []string{}
	for _, component := range clusterCatalog {
		if component.Core || selected[component.ID] {
			resolved = append(resolved, component.ID)
		}
	}
	for _, component := range clusterCatalog {
		if !slices.Contains(resolved, component.ID) || len(component.RequiresAny) == 0 {
			continue
		}
		if !slices.ContainsFunc(component.RequiresAny, func(id string) bool { return slices.Contains(resolved, id) }) {
			titles := make([]string, 0, len(component.RequiresAny))
			for _, id := range component.RequiresAny {
				titles = append(titles, ClusterComponentTitle(id))
			}
			return nil, fmt.Errorf("%s needs one of: %s.", component.Title, strings.Join(titles, ", "))
		}
	}
	return resolved, nil
}

// SavedClusterComponents returns the components of a saved cluster, defaulting legacy clusters
// that predate component selection to the full set they were provisioned with.
func SavedClusterComponents(saved []string, networking string) []string {
	if len(saved) == 0 {
		return DefaultClusterComponents(networking)
	}
	return slices.Clone(saved)
}

// RegistryNeeded reports whether an upstream registry serves images for the selected
// components; registries no catalog component claims are always needed.
func RegistryNeeded(source string, components []string) bool {
	claimed := false
	for _, component := range clusterCatalog {
		if !slices.Contains(component.Registries, source) {
			continue
		}
		claimed = true
		if slices.Contains(components, component.ID) {
			return true
		}
	}
	return !claimed
}

// KnownClusterComponent reports whether id names a cluster component.
func KnownClusterComponent(id string) bool {
	return slices.ContainsFunc(clusterCatalog, func(component ClusterComponent) bool { return component.ID == id })
}

// ClusterComponentTitle returns the display name of a cluster component.
func ClusterComponentTitle(id string) string {
	for _, component := range clusterCatalog {
		if component.ID == id {
			return component.Title
		}
	}
	return id
}
