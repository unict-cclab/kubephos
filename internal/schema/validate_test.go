package schema

import (
	"encoding/json"
	"testing"
)

func TestValidateRequiredTypesAndBounds(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","required":["name","count"],"additionalProperties":false,"properties":{"name":{"type":"string","minLength":2},"count":{"type":"integer","minimum":1}}}`)
	issues, err := Validate(definition, json.RawMessage(`{"name":"x","count":0,"extra":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(issues))
	}
}

func TestValidateAcceptsMatchingObject(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","required":["endpoint"],"properties":{"endpoint":{"type":"string","format":"uri"}}}`)
	issues, err := Validate(definition, json.RawMessage(`{"endpoint":"https://example.test:8006"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %#v", issues)
	}
}

func TestValidatePatternsAndOpaqueReferences(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","pattern":"^[a-z]+$"},"credential":{"type":"string","format":"kubephos-secret-ref"},"connection":{"type":"string","format":"kubephos-connection-ref"},"application":{"type":"string","format":"kubephos-application-ref"},"artifact":{"type":"string","format":"kubephos-artifact-ref"}}}`)
	issues, err := Validate(definition, json.RawMessage(`{"name":"Invalid-1","credential":"plain","connection":"cred_wrong","application":"not-an-app","artifact":"not-an-artifact"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 5 {
		t.Fatalf("expected 5 issues, got %#v", issues)
	}
}

func TestValidateDefinitionAcceptsSupportedSchema(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","required":["name"],"additionalProperties":false,"properties":{"name":{"type":"string","pattern":"^[a-z]+$","default":"ready"},"replicas":{"type":"integer","minimum":1,"maximum":10,"default":2},"targets":{"type":"array","maxItems":4,"items":{"type":"string"}}}}`)
	if err := ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDefinitionRejectsUnsupportedAndInvalidRules(t *testing.T) {
	values := []json.RawMessage{
		json.RawMessage(`{"type":"object","unknown":true}`),
		json.RawMessage(`{"type":"object","required":["missing"],"properties":{}}`),
		json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","pattern":"["}}}`),
		json.RawMessage(`{"type":"object","properties":{"replicas":{"type":"integer","minimum":2,"default":1}}}`),
	}
	for _, value := range values {
		if err := ValidateDefinition(value); err == nil {
			t.Fatalf("expected invalid definition: %s", value)
		}
	}
}

func TestValidateDefinitionChecksPrimaryActions(t *testing.T) {
	valid := json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["start","stop"],"x-kubephos-primary-action":true}}}`)
	if err := ValidateDefinition(valid); err != nil {
		t.Fatalf("expected primary action to be valid: %v", err)
	}
	invalid := json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","x-kubephos-primary-action":true}}}`)
	if err := ValidateDefinition(invalid); err == nil {
		t.Fatal("expected primary action without enum to fail")
	}
}

func TestValidateDefinitionChecksMultilineFields(t *testing.T) {
	valid := json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","x-kubephos-multiline":true}}}`)
	if err := ValidateDefinition(valid); err != nil {
		t.Fatalf("expected multiline field to be valid: %v", err)
	}
	invalid := json.RawMessage(`{"type":"object","properties":{"command":{"type":"integer","x-kubephos-multiline":true}}}`)
	if err := ValidateDefinition(invalid); err == nil {
		t.Fatal("expected multiline non-string field to fail")
	}
}
