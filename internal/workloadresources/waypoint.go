package workloadresources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"

	"kubephos.dev/kubephos/internal/placement"
)

const (
	// MaxWaypointReplicas bounds the Istio ambient waypoint of a run.
	MaxWaypointReplicas = 10
	// MaxWaypointServiceOverrides bounds the services listed as waypoint exceptions.
	MaxWaypointServiceOverrides = 200
)

// Waypoint configures the Istio ambient waypoint of a namespace: how many replicas
// it runs, the resources of each replica's proxy, the nodes it runs on, and which
// services use it. A service that does not use the waypoint stays in the ambient
// mesh (ztunnel) but loses L7 processing and request metrics.
type Waypoint struct {
	Replicas  int
	Resources map[string]any
	// NodeSelector and Tolerations place the waypoint pods, with the same rules as
	// application placement: a selector such as kubernetes.io/hostname pins a node.
	NodeSelector map[string]string
	Tolerations  []placement.Toleration
	// AllServices routes every service through the waypoint by default.
	AllServices bool
	// ServiceOverrides lists application services that differ from AllServices.
	ServiceOverrides map[string]bool
}

// DefaultSize reports whether the waypoint runs with Istio's replicas and resources.
func (waypoint Waypoint) DefaultSize() bool {
	return waypoint.Replicas == 0 && len(waypoint.Resources) == 0
}

// DefaultDeployment reports whether Istio's generated waypoint Deployment is used
// unchanged: default size and no placement.
func (waypoint Waypoint) DefaultDeployment() bool {
	return waypoint.DefaultSize() && len(waypoint.NodeSelector) == 0 && len(waypoint.Tolerations) == 0
}

// ParseWaypoint reads `replicas`, the resource fields shared with workloads
// (cpuRequest, cpuLimit, memoryRequest, memoryLimit), `nodeSelector` and
// `tolerations` (validated like application placement), `allServices` (true when
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
		case key == "nodeSelector":
			selector, ok := value.(map[string]any)
			if !ok {
				return Waypoint{}, fmt.Errorf("Waypoint node selector must map label keys to values.")
			}
			result.NodeSelector = make(map[string]string, len(selector))
			for label, labelValue := range selector {
				text, ok := labelValue.(string)
				if !ok {
					return Waypoint{}, fmt.Errorf("Waypoint node selector value for %q must be text.", label)
				}
				result.NodeSelector[label] = text
			}
			if err := placement.ValidateLabels(result.NodeSelector, false); err != nil {
				return Waypoint{}, fmt.Errorf("Waypoint node selector: %w", err)
			}
		case key == "tolerations":
			raw, err := json.Marshal(value)
			if err != nil {
				return Waypoint{}, fmt.Errorf("Waypoint tolerations are invalid.")
			}
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&result.Tolerations); err != nil {
				return Waypoint{}, fmt.Errorf("Waypoint tolerations must be a list of tolerations.")
			}
			if err := placement.ValidateTolerations(result.Tolerations); err != nil {
				return Waypoint{}, fmt.Errorf("Waypoint tolerations: %w", err)
			}
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
