package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// clusterComponentSet returns the components installed on a saved cluster. Clusters
// created before component selection run the full set for their networking mode.
func clusterComponentSet(cluster domain.ManagedResource) map[string]bool {
	var spec managedKubernetesClusterInput
	_ = json.Unmarshal(cluster.Spec, &spec)
	installed := map[string]bool{}
	for _, id := range managedcomponents.SavedClusterComponents(spec.Components, clusterNetworking(cluster)) {
		installed[id] = true
	}
	return installed
}

// withInstalledComponents fills in the component list of clusters created before
// component selection, so clients always see what each cluster and member runs.
func withInstalledComponents(resource domain.ManagedResource) domain.ManagedResource {
	var spec map[string]any
	if json.Unmarshal(resource.Spec, &spec) != nil || spec == nil {
		return resource
	}
	fill := func(value map[string]any, networking string) {
		if _, saved := value["components"]; !saved {
			value["components"] = managedcomponents.DefaultClusterComponents(networking)
		}
	}
	fill(spec, clusterNetworking(resource))
	if members, ok := spec["members"].([]any); ok {
		for _, member := range members {
			if value, ok := member.(map[string]any); ok {
				fill(value, "cilium")
			}
		}
	}
	resource.Spec, _ = json.Marshal(spec)
	return resource
}

// clusterNetworking returns the networking mode of a saved cluster; multi clusters use Cilium.
func clusterNetworking(cluster domain.ManagedResource) string {
	var spec managedKubernetesClusterInput
	_ = json.Unmarshal(cluster.Spec, &spec)
	if spec.Mode == "multi" {
		return "cilium"
	}
	return spec.Networking
}

// istioSidecarRequested reports whether the definition explicitly asks for sidecar proxies.
func istioSidecarRequested(definition experimentConfigurationDefinitionInput) bool {
	if definition.IstioDataPlane == "ambient" {
		return false
	}
	if definition.IstioInjection != nil && *definition.IstioInjection {
		return true
	}
	for _, enabled := range definition.IstioInjectionOverrides {
		if enabled {
			return true
		}
	}
	return len(definition.IstioProxyResources) > 0
}

// validateClusterComponents rejects Istio modes and plugins whose components the cluster lacks.
func (s *Server) validateClusterComponents(cluster domain.ManagedResource, definition experimentConfigurationDefinitionInput) *managedOperationFailure {
	installed := clusterComponentSet(cluster)
	missing := func(subject, component string) *managedOperationFailure {
		return &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "component_unavailable", message: fmt.Sprintf("%s needs %s, which is not installed on cluster %s.", subject, managedcomponents.ClusterComponentTitle(component), cluster.Name)}
	}
	// Cilium clusters never run Istio and already ignore its settings.
	if definition.DestinationMode != "external" && clusterNetworking(cluster) != "cilium" {
		if definition.IstioDataPlane == "ambient" && !installed[managedcomponents.IstioAmbient] {
			return missing("Istio ambient mode", managedcomponents.IstioAmbient)
		}
		if istioSidecarRequested(definition) && !installed[managedcomponents.IstioSidecar] {
			return missing("Istio sidecar injection", managedcomponents.IstioSidecar)
		}
	}
	if s.registry == nil {
		return nil
	}
	for _, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			continue
		}
		manifest := plugin.Manifest()
		for _, required := range manifest.RequiredComponents {
			if !installed[required] {
				return missing(manifest.Name, required)
			}
		}
	}
	return nil
}

// defaultIstioInjection disables sidecar injection by default on clusters without Istio sidecars.
func defaultIstioInjection(cluster domain.ManagedResource, definition experimentConfigurationDefinitionInput) *bool {
	if definition.IstioInjection != nil || clusterComponentSet(cluster)[managedcomponents.IstioSidecar] {
		return definition.IstioInjection
	}
	disabled := false
	return &disabled
}
