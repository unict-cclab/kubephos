package workloadresources

import (
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

var quantity = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?)(m|[KMGTPE]i|[kKMGTPE])?$`)

// Build omits unspecified resources and validates the quantities used by generators.
func Build(values map[string]any) (map[string]any, error) {
	result := map[string]any{}
	parsed := map[string]*big.Rat{}
	for _, kind := range []string{"Request", "Limit"} {
		fields := map[string]any{}
		for _, resource := range []string{"cpu", "memory"} {
			key := resource + kind
			value, exists := values[key]
			if !exists || value == nil || value == "" {
				continue
			}
			text, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a Kubernetes quantity", key)
			}
			match := quantity.FindStringSubmatch(text)
			if match == nil {
				return nil, fmt.Errorf("%s must be a Kubernetes quantity such as 500m or 512Mi", key)
			}
			number, _ := new(big.Rat).SetString(match[1])
			suffix := match[2]
			switch {
			case suffix == "m":
				number.Quo(number, big.NewRat(1000, 1))
			case suffix != "":
				power := strings.Index("KMGTPE", strings.ToUpper(suffix[:1])) + 1
				base := int64(1000)
				if strings.HasSuffix(suffix, "i") {
					base = 1024
				}
				factor := new(big.Int).Exp(big.NewInt(base), big.NewInt(int64(power)), nil)
				number.Mul(number, new(big.Rat).SetInt(factor))
			}
			if kind == "Limit" && number.Sign() == 0 {
				return nil, fmt.Errorf("%s must be greater than zero", key)
			}
			parsed[key] = number
			fields[resource] = text
		}
		if len(fields) > 0 {
			field := "requests"
			if kind == "Limit" {
				field = "limits"
			}
			result[field] = fields
		}
	}
	for _, resource := range []string{"cpu", "memory"} {
		request, limit := parsed[resource+"Request"], parsed[resource+"Limit"]
		if request != nil && limit != nil && request.Cmp(limit) > 0 {
			return nil, fmt.Errorf("%s request cannot exceed its limit", resource)
		}
	}
	return result, nil
}

var Fields = []string{"cpuRequest", "cpuLimit", "memoryRequest", "memoryLimit"}

// ForService applies only the explicitly provided service overrides.
func ForService(values map[string]any, name string) (map[string]any, error) {
	effective := make(map[string]any, len(Fields))
	for _, field := range Fields {
		if value, exists := values[field]; exists {
			effective[field] = value
		}
	}
	if overrides, ok := values["resourceOverrides"].(map[string]any); ok {
		if override, ok := overrides[name].(map[string]any); ok {
			for field, value := range override {
				effective[field] = value
			}
		}
	}
	resources, err := Build(effective)
	if err != nil {
		return nil, fmt.Errorf("resources for %s: %w", name, err)
	}
	return resources, nil
}

func Validate(values map[string]any, services []string) error {
	if _, err := Build(values); err != nil {
		return err
	}
	overrides, exists := values["resourceOverrides"]
	if !exists {
		return nil
	}
	items, ok := overrides.(map[string]any)
	if !ok {
		return fmt.Errorf("resource overrides must be an object keyed by service")
	}
	known := map[string]bool{}
	for _, name := range services {
		known[name] = true
	}
	allowed := map[string]bool{}
	for _, field := range Fields {
		allowed[field] = true
	}
	for name, value := range items {
		if !known[name] {
			return fmt.Errorf("resource override references unknown service %s", name)
		}
		override, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("resource override for %s must be an object", name)
		}
		for field := range override {
			if !allowed[field] {
				return fmt.Errorf("unknown resource field %s for %s", field, name)
			}
		}
		if _, err := ForService(values, name); err != nil {
			return err
		}
	}
	return nil
}
