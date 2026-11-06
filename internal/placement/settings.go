package placement

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value,omitempty"`
	Effect string `json:"effect"`
}
type Toleration struct {
	Key               string `json:"key,omitempty"`
	Operator          string `json:"operator,omitempty"`
	Value             string `json:"value,omitempty"`
	Effect            string `json:"effect,omitempty"`
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}
type PoolSettings struct {
	Labels map[string]string `json:"labels,omitempty"`
	Taints []Taint           `json:"taints,omitempty"`
}
type ServiceOverride struct {
	NodeSelectorMode string            `json:"nodeSelectorMode,omitempty"`
	NodeSelector     map[string]string `json:"nodeSelector,omitempty"`
	TolerationsMode  string            `json:"tolerationsMode,omitempty"`
	Tolerations      []Toleration      `json:"tolerations,omitempty"`
}
type ApplicationSettings struct {
	NodeSelector     map[string]string          `json:"nodeSelector,omitempty"`
	Tolerations      []Toleration               `json:"tolerations,omitempty"`
	ServiceOverrides map[string]ServiceOverride `json:"serviceOverrides,omitempty"`
}

var labelName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_.-]{0,61}[A-Za-z0-9])?$`)
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

func ValidKey(key string) bool {
	parts := strings.Split(key, "/")
	if len(parts) > 2 || !labelName.MatchString(parts[len(parts)-1]) {
		return false
	}
	if len(parts) == 2 {
		if len(parts[0]) > 253 {
			return false
		}
		for _, segment := range strings.Split(parts[0], ".") {
			if !dnsLabel.MatchString(segment) {
				return false
			}
		}
	}
	return true
}
func ValidValue(value string) bool { return value == "" || labelName.MatchString(value) }
func ManagedKey(key string) bool {
	for _, prefix := range []string{"kubephos.dev/", "kubernetes.io/", "node-role.kubernetes.io/", "topology.kubernetes.io/"} {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}
func ValidateLabels(values map[string]string, protectManaged bool) error {
	for key, value := range values {
		if !ValidKey(key) || !ValidValue(value) {
			return fmt.Errorf("Invalid label or selector %q. Use a Kubernetes label key and a value of at most 63 characters.", key)
		}
		if protectManaged && ManagedKey(key) {
			return fmt.Errorf("Label %q is managed by KubePhos or Kubernetes.", key)
		}
	}
	return nil
}
func ValidEffect(effect string) bool {
	return effect == "NoSchedule" || effect == "PreferNoSchedule" || effect == "NoExecute"
}
func ManagedTaintKey(key string) bool {
	return ManagedKey(key) || strings.HasPrefix(key, "node.kubernetes.io/") || strings.HasPrefix(key, "node.cloudprovider.kubernetes.io/")
}
func ValidatePool(settings PoolSettings) error {
	if err := ValidateLabels(settings.Labels, true); err != nil {
		return err
	}
	if len(settings.Taints) > 64 {
		return fmt.Errorf("Use at most 64 taints per pool.")
	}
	seen := map[string]bool{}
	for _, t := range settings.Taints {
		if !ValidKey(t.Key) || !ValidValue(t.Value) || !ValidEffect(t.Effect) {
			return fmt.Errorf("Taint %q requires a valid key, value and effect.", t.Key)
		}
		if ManagedTaintKey(t.Key) {
			return fmt.Errorf("Taint %q is managed by KubePhos or Kubernetes.", t.Key)
		}
		key := t.Key + "/" + t.Effect
		if seen[key] {
			return fmt.Errorf("Taint %q with effect %s is duplicated.", t.Key, t.Effect)
		}
		seen[key] = true
	}
	return nil
}
func ValidateTolerations(values []Toleration) error {
	if len(values) > 64 {
		return fmt.Errorf("Use at most 64 tolerations.")
	}
	seen := map[string]bool{}
	for _, value := range values {
		operator := value.Operator
		if operator == "" {
			operator = "Equal"
		}
		if operator != "Equal" && operator != "Exists" {
			return fmt.Errorf("Toleration operator must be Equal or Exists.")
		}
		if value.Key == "" && operator != "Exists" || value.Key != "" && !ValidKey(value.Key) {
			return fmt.Errorf("A toleration requires a valid key; an empty key is allowed only with Exists.")
		}
		if operator == "Exists" && value.Value != "" || !ValidValue(value.Value) {
			return fmt.Errorf("An Exists toleration must have no value; other toleration values must be valid label values.")
		}
		if value.Effect != "" && !ValidEffect(value.Effect) {
			return fmt.Errorf("Select a valid toleration effect.")
		}
		if value.TolerationSeconds != nil && (*value.TolerationSeconds < 0 || value.Effect != "NoExecute") {
			return fmt.Errorf("Toleration time must be nonnegative and can be set only for NoExecute.")
		}
		value.Operator = operator
		raw, _ := json.Marshal(value)
		if seen[string(raw)] {
			return fmt.Errorf("The same toleration is specified more than once.")
		}
		seen[string(raw)] = true
	}
	return nil
}
func ValidateApplication(value *ApplicationSettings) error {
	if value == nil {
		return nil
	}
	if err := ValidateLabels(value.NodeSelector, false); err != nil {
		return err
	}
	if err := ValidateTolerations(value.Tolerations); err != nil {
		return err
	}
	if len(value.ServiceOverrides) > 500 {
		return fmt.Errorf("Too many service placement overrides.")
	}
	for service, override := range value.ServiceOverrides {
		for _, mode := range []string{override.NodeSelectorMode, override.TolerationsMode} {
			if mode != "" && mode != "inherit" && mode != "custom" && mode != "none" {
				return fmt.Errorf("Invalid placement mode for service %s.", service)
			}
		}
		if err := ValidateLabels(override.NodeSelector, false); err != nil {
			return fmt.Errorf("%s: %w", service, err)
		}
		if err := ValidateTolerations(override.Tolerations); err != nil {
			return fmt.Errorf("%s: %w", service, err)
		}
	}
	return nil
}
func Effective(settings *ApplicationSettings, service string) (map[string]string, []Toleration) {
	if settings == nil {
		return nil, nil
	}
	selector, tolerations := settings.NodeSelector, settings.Tolerations
	override := settings.ServiceOverrides[service]
	switch override.NodeSelectorMode {
	case "none":
		selector = nil
	case "custom":
		selector = override.NodeSelector
	}
	switch override.TolerationsMode {
	case "none":
		tolerations = nil
	case "custom":
		tolerations = override.Tolerations
	}
	return selector, tolerations
}

// WorkloadDefaults describes the manifest's existing pod placement before user
// overrides and before Kubernetes admission adds its own defaults.
type WorkloadDefaults struct {
	ID           string            `json:"id"`
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	Tolerations  []Toleration      `json:"tolerations,omitempty"`
	NodeBinding  string            `json:"nodeBinding,omitempty"`
}
