package managedplatform

import "bytes"

func platformNamespaces(networking string) []string {
	if networking == "cilium" {
		return []string{chaosNamespace, systemNamespace}
	}
	return []string{istioNamespace, chaosNamespace, systemNamespace}
}
func platformComponentCount(networking string) int {
	if networking == "cilium" {
		return 9
	}
	return 10
}
func platformEndpointCount(string) int { return 6 }

func ciliumPlatformCapability(value managedPlatformCapability) managedPlatformCapability {
	components := []managedComponent{}
	for _, component := range value.Spec.Components {
		if component.Name != "istio" && component.Name != "kiali" {
			components = append(components, component)
		}
	}
	value.Spec.Components = append(components, managedComponent{Name: "cilium", Version: "1.20.3", Namespace: "kube-system", Status: "ready"})
	endpoints := []managedEndpoint{}
	for _, endpoint := range value.Spec.Endpoints {
		if endpoint.Name != "kiali" {
			endpoints = append(endpoints, endpoint)
		}
	}
	value.Spec.Endpoints = append(endpoints, managedEndpoint{Name: "hubble", Namespace: "kube-system", Service: "hubble-ui", Scheme: "http", NodePort: 31235})
	value.Metadata.Version = "cilium.1.20.3+chaos." + chaosVersion + "+cpa." + cpaVersion + "+mentat." + mentatVersion + "+lens." + clusterLensVersion + "+headlamp." + headlampVersion
	return value
}

func networkObservabilityManifest(marker, networking string) []byte {
	value := extendedObservabilityManifest(marker)
	if networking == "cilium" {
		value = bytes.Split(value, []byte("---\napiVersion: telemetry.istio.io"))[0]
	}
	return value
}
