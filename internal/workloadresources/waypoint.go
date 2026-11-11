package workloadresources

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

const (
	// MaxWaypointReplicas bounds the Istio ambient waypoint of a run.
	MaxWaypointReplicas = 10
	// MaxWaypointServiceOverrides bounds the services listed as waypoint exceptions.
	MaxWaypointServiceOverrides = 200
)

// Waypoint configures the Istio ambient waypoint of a namespace: how many replicas
// it runs, the resources of each replica's proxy, and which services use it.
// A service that does not use the waypoint stays in the ambient mesh (ztunnel)
// but loses L7 processing and request metrics.
type Waypoint struct {
	Replicas  int
	Resources map[string]any
	// AllServices routes every service through the waypoint by default.
	AllServices bool
	// ServiceOverrides lists application services that differ from AllServices.
	ServiceOverrides map[string]bool
}

// DefaultSize reports whether the waypoint runs with Istio's replicas and resources.
func (waypoint Waypoint) DefaultSize() bool {
	return waypoint.Replicas == 0 && len(waypoint.Resources) == 0
}

// ParseWaypoint reads `replicas`, the resource fields shared with workloads
// (cpuRequest, cpuLimit, memoryRequest, memoryLimit), `allServices` (true when
// absent) and `serviceOverrides`, keyed by application service.
func ParseWaypoint(values map[string]any) (Waypoint, error) {
	result := Waypoint{AllServices: true}
	resources := map[string]any{}
	for key, value := range values {
		switch {
		case key == "replicas":
			replicas, ok := wholeNumber(value)
			if !ok || replicas < 1 || replicas > MaxWaypointReplicas {
				return Waypoint{}, fmt.Errorf("Waypoint replicas must be a whole number from 1 to %d.", MaxWaypointReplicas)
			}
			result.Replicas = replicas
		case key == "allServices":
			all, ok := value.(bool)
			if !ok {
				return Waypoint{}, fmt.Errorf("Waypoint allServices must be true or false.")
			}
			result.AllServices = all
		case key == "serviceOverrides":
			overrides, ok := value.(map[string]any)
			if !ok || len(overrides) > MaxWaypointServiceOverrides {
				return Waypoint{}, fmt.Errorf("Waypoint service overrides must list at most %d services.", MaxWaypointServiceOverrides)
			}
			result.ServiceOverrides = make(map[string]bool, len(overrides))
			for service, use := range overrides {
				enabled, ok := use.(bool)
				if !ok || service == "" {
					return Waypoint{}, fmt.Errorf("Waypoint override for %q must be true or false.", service)
				}
				result.ServiceOverrides[service] = enabled
			}
		case slices.Contains(Fields, key):
			resources[key] = value
		default:
			return Waypoint{}, fmt.Errorf("Unknown waypoint field %s", key)
		}
	}
	built, err := Build(resources)
	if err != nil {
		return Waypoint{}, fmt.Errorf("waypoint %w", err)
	}
	if len(built) > 0 {
		result.Resources = built
	}
	return result, nil
}

func wholeNumber(value any) (int, bool) {
	switch number := value.(type) {
	case int:
		return number, true
	case float64:
		return int(number), number == math.Trunc(number) && !math.IsInf(number, 0)
	case json.Number:
		parsed, err := number.Int64()
		return int(parsed), err == nil
	default:
		return 0, false
	}
}
