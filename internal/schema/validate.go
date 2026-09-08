package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

type Issue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

func Validate(definition, raw json.RawMessage) ([]Issue, error) {
	var rule map[string]any
	if err := json.Unmarshal(definition, &rule); err != nil {
		return nil, fmt.Errorf("invalid schema: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return []Issue{{Path: "$", Message: "Value must be valid JSON."}}, nil
	}
	return validateValue(rule, value, "$"), nil
}

func validateValue(rule map[string]any, value any, path string) []Issue {
	issues := []Issue{}
	typeName, _ := rule["type"].(string)
	if typeName != "" && !matchesType(typeName, value) {
		return []Issue{{Path: path, Message: fmt.Sprintf("Value must be %s.", typeName)}}
	}
	if options, ok := rule["enum"].([]any); ok {
		matched := false
		for _, option := range options {
			if reflect.DeepEqual(option, value) || fmt.Sprint(option) == fmt.Sprint(value) {
				matched = true
			}
		}
		if !matched {
			issues = append(issues, Issue{Path: path, Message: "Value is not one of the allowed options."})
		}
	}
	switch current := value.(type) {
	case map[string]any:
		required := stringSet(rule["required"])
		for name := range required {
			if _, ok := current[name]; !ok {
				issues = append(issues, Issue{Path: path + "." + name, Message: "Value is required."})
			}
		}
		properties, _ := rule["properties"].(map[string]any)
		for name, item := range current {
			property, exists := properties[name]
			if !exists {
				if allowed, ok := rule["additionalProperties"].(bool); ok && !allowed {
					issues = append(issues, Issue{Path: path + "." + name, Message: "Property is not allowed."})
				}
				continue
			}
			propertyRule, ok := property.(map[string]any)
			if ok {
				issues = append(issues, validateValue(propertyRule, item, path+"."+name)...)
			}
		}
	case []any:
		if minimum, ok := number(rule["minItems"]); ok && float64(len(current)) < minimum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("At least %d items are required.", int(minimum))})
		}
		if maximum, ok := number(rule["maxItems"]); ok && float64(len(current)) > maximum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("At most %d items are allowed.", int(maximum))})
		}
		if itemRule, ok := rule["items"].(map[string]any); ok {
			for position, item := range current {
				issues = append(issues, validateValue(itemRule, item, fmt.Sprintf("%s[%d]", path, position))...)
			}
		}
	case string:
		if minimum, ok := number(rule["minLength"]); ok && float64(len([]rune(current))) < minimum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("Value must contain at least %d characters.", int(minimum))})
		}
		if maximum, ok := number(rule["maxLength"]); ok && float64(len([]rune(current))) > maximum {
			issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("Value cannot exceed %d characters.", int(maximum))})
		}
		if pattern, ok := rule["pattern"].(string); ok {
			compiled, err := regexp.Compile(pattern)
			if err != nil {
				issues = append(issues, Issue{Path: path, Message: "Schema contains an invalid pattern."})
			} else if !compiled.MatchString(current) {
				issues = append(issues, Issue{Path: path, Message: "Value does not match the required pattern."})
			}
		}
		switch format, _ := rule["format"].(string); format {
		case "uri":
			parsed, err := url.Parse(current)
			if err != nil || parsed.Scheme == "" || parsed.Host == "" {
				issues = append(issues, Issue{Path: path, Message: "Value must be an absolute URI."})
			}
		case "kubephos-secret-ref":
			if !strings.HasPrefix(current, "cred_") {
				issues = append(issues, Issue{Path: path, Message: "Value must reference a stored credential."})
			}
		case "kubephos-connection-ref":
			if !strings.HasPrefix(current, "conn_") {
				issues = append(issues, Issue{Path: path, Message: "Value must reference a stored provider connection."})
			}
		case "kubephos-application-ref":
			if !strings.HasPrefix(current, "app:") || !strings.Contains(strings.TrimPrefix(current, "app:"), "@") {
				issues = append(issues, Issue{Path: path, Message: "Value must reference a catalog application version."})
			}
		}
	case json.Number:
		value, err := current.Float64()
		if err == nil {
			if minimum, ok := number(rule["minimum"]); ok && value < minimum {
				issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("Value must be at least %v.", minimum)})
			}
			if maximum, ok := number(rule["maximum"]); ok && value > maximum {
				issues = append(issues, Issue{Path: path, Message: fmt.Sprintf("Value cannot exceed %v.", maximum)})
			}
		}
	}
	return issues
}

func matchesType(expected string, value any) bool {
	switch expected {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		parsed, err := number.Float64()
		return err == nil && parsed == math.Trunc(parsed)
	case "null":
		return value == nil
	default:
		return true
	}
}

func stringSet(value any) map[string]bool {
	result := map[string]bool{}
	items, _ := value.([]any)
	for _, item := range items {
		if current, ok := item.(string); ok && strings.TrimSpace(current) != "" {
			result[current] = true
		}
	}
	return result
}

func number(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case json.Number:
		parsed, err := current.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
