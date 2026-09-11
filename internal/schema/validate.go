package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"reflect"
	"regexp"
	"strings"
)

var supportedKeywords = map[string]bool{
	"type": true, "title": true, "description": true, "default": true, "enum": true,
	"required": true, "properties": true, "additionalProperties": true, "items": true,
	"minimum": true, "maximum": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true, "pattern": true, "format": true, "writeOnly": true,
}

func ValidateDefinition(definition json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(definition))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return fmt.Errorf("invalid schema: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("schema must contain one JSON value")
	}
	rule, ok := value.(map[string]any)
	if !ok {
		return errors.New("schema root must be an object")
	}
	return validateRule(rule, "$")
}

func validateRule(rule map[string]any, path string) error {
	for keyword := range rule {
		if !supportedKeywords[keyword] && !strings.HasPrefix(keyword, "x-kubephos-") {
			return fmt.Errorf("%s.%s is not supported", path, keyword)
		}
	}
	typeName := ""
	if value, exists := rule["type"]; exists {
		var ok bool
		typeName, ok = value.(string)
		if !ok || !map[string]bool{"object": true, "array": true, "string": true, "boolean": true, "number": true, "integer": true, "null": true}[typeName] {
			return fmt.Errorf("%s.type is invalid", path)
		}
	}
	properties := map[string]any{}
	if value, exists := rule["properties"]; exists {
		var ok bool
		properties, ok = value.(map[string]any)
		if !ok || (typeName != "" && typeName != "object") {
			return fmt.Errorf("%s.properties requires an object schema", path)
		}
		for name, property := range properties {
			propertyRule, ok := property.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.properties.%s must be an object", path, name)
			}
			if err := validateRule(propertyRule, path+".properties."+name); err != nil {
				return err
			}
		}
	}
	if value, exists := rule["required"]; exists {
		items, ok := value.([]any)
		if !ok || (typeName != "" && typeName != "object") {
			return fmt.Errorf("%s.required requires an object schema", path)
		}
		seen := map[string]bool{}
		for _, item := range items {
			name, ok := item.(string)
			if !ok || name == "" || seen[name] {
				return fmt.Errorf("%s.required is invalid", path)
			}
			if _, exists := properties[name]; !exists {
				return fmt.Errorf("%s.required references unknown property %q", path, name)
			}
			seen[name] = true
		}
	}
	if value, exists := rule["items"]; exists {
		itemRule, ok := value.(map[string]any)
		if !ok || (typeName != "" && typeName != "array") {
			return fmt.Errorf("%s.items requires an array schema", path)
		}
		if err := validateRule(itemRule, path+".items"); err != nil {
			return err
		}
	}
	if value, exists := rule["pattern"]; exists {
		pattern, ok := value.(string)
		if !ok || (typeName != "" && typeName != "string") {
			return fmt.Errorf("%s.pattern requires a string schema", path)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("%s.pattern is invalid", path)
		}
	}
	if value, exists := rule["additionalProperties"]; exists {
		if _, ok := value.(bool); !ok || (typeName != "" && typeName != "object") {
			return fmt.Errorf("%s.additionalProperties requires an object schema", path)
		}
	}
	if value, exists := rule["enum"]; exists {
		items, ok := value.([]any)
		if !ok || len(items) == 0 {
			return fmt.Errorf("%s.enum must contain at least one value", path)
		}
		for _, item := range items {
			if typeName != "" && !matchesType(typeName, item) {
				return fmt.Errorf("%s.enum contains a value that does not match type %s", path, typeName)
			}
		}
	}
	for _, keyword := range []string{"title", "description", "format"} {
		if value, exists := rule[keyword]; exists {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("%s.%s must be a string", path, keyword)
			}
		}
	}
	if value, exists := rule["writeOnly"]; exists {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s.writeOnly must be a boolean", path)
		}
	}
	if value, exists := rule["x-kubephos-primary-action"]; exists {
		enabled, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%s.x-kubephos-primary-action must be a boolean", path)
		}
		if enabled {
			if _, ok := rule["enum"].([]any); !ok || (typeName != "string" && typeName != "integer" && typeName != "number") {
				return fmt.Errorf("%s.x-kubephos-primary-action requires a string or numeric enum", path)
			}
		}
	}
	if value, exists := rule["x-kubephos-multiline"]; exists {
		enabled, ok := value.(bool)
		if !ok || enabled && typeName != "string" {
			return fmt.Errorf("%s.x-kubephos-multiline requires a boolean string schema", path)
		}
	}
	for _, keyword := range []string{"minimum", "maximum"} {
		if value, exists := rule[keyword]; exists {
			if _, ok := number(value); !ok || (typeName != "" && typeName != "integer" && typeName != "number") {
				return fmt.Errorf("%s.%s requires a numeric schema", path, keyword)
			}
		}
	}
	if minimum, ok := number(rule["minimum"]); ok {
		if maximum, exists := number(rule["maximum"]); exists && minimum > maximum {
			return fmt.Errorf("%s.minimum cannot exceed maximum", path)
		}
	}
	for _, keyword := range []string{"minLength", "maxLength"} {
		if value, exists := rule[keyword]; exists {
			if _, ok := nonNegativeInteger(value); !ok || (typeName != "" && typeName != "string") {
				return fmt.Errorf("%s.%s requires a non-negative integer on a string schema", path, keyword)
			}
		}
	}
	if minimum, ok := nonNegativeInteger(rule["minLength"]); ok {
		if maximum, exists := nonNegativeInteger(rule["maxLength"]); exists && minimum > maximum {
			return fmt.Errorf("%s.minLength cannot exceed maxLength", path)
		}
	}
	for _, keyword := range []string{"minItems", "maxItems"} {
		if value, exists := rule[keyword]; exists {
			if _, ok := nonNegativeInteger(value); !ok || (typeName != "" && typeName != "array") {
				return fmt.Errorf("%s.%s requires a non-negative integer on an array schema", path, keyword)
			}
		}
	}
	if minimum, ok := nonNegativeInteger(rule["minItems"]); ok {
		if maximum, exists := nonNegativeInteger(rule["maxItems"]); exists && minimum > maximum {
			return fmt.Errorf("%s.minItems cannot exceed maxItems", path)
		}
	}
	if value, exists := rule["default"]; exists {
		if issues := validateValue(rule, value, path+".default"); len(issues) > 0 {
			return fmt.Errorf("%s: %s", issues[0].Path, issues[0].Message)
		}
	}
	return nil
}

func nonNegativeInteger(value any) (float64, bool) {
	current, ok := number(value)
	return current, ok && current >= 0 && current == math.Trunc(current)
}

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
		case "kubephos-artifact-ref":
			if !strings.HasPrefix(current, "art_") {
				issues = append(issues, Issue{Path: path, Message: "Value must reference a verified artifact."})
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
