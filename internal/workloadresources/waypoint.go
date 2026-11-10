package workloadresources

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
)

// MaxWaypointReplicas bounds the Istio ambient waypoint of a run.
const MaxWaypointReplicas = 10

// Waypoint sizes the Istio ambient waypoint of a run: how many replicas it runs
// and the resources of each replica's proxy. Zero values keep Istio's defaults.
type Waypoint struct {
	Replicas  int
	Resources map[string]any
}

// IsDefault reports whether the settings leave the waypoint as Istio creates it.
func (waypoint Waypoint) IsDefault() bool {
	return waypoint.Replicas == 0 && len(waypoint.Resources) == 0
}

// ParseWaypoint reads `replicas` and the resource fields shared with workloads
// (cpuRequest, cpuLimit, memoryRequest, memoryLimit) from configuration values.
func ParseWaypoint(values map[string]any) (Waypoint, error) {
	var result Waypoint
	resources := map[string]any{}
	for key, value := range values {
		switch {
		case key == "replicas":
			replicas, ok := wholeNumber(value)
			if !ok || replicas < 1 || replicas > MaxWaypointReplicas {
				return Waypoint{}, fmt.Errorf("Waypoint replicas must be a whole number from 1 to %d.", MaxWaypointReplicas)
			}
			result.Replicas = replicas
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
