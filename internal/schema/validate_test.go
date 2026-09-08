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
	definition := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string","pattern":"^[a-z]+$"},"credential":{"type":"string","format":"kubephos-secret-ref"},"connection":{"type":"string","format":"kubephos-connection-ref"},"application":{"type":"string","format":"kubephos-application-ref"}}}`)
	issues, err := Validate(definition, json.RawMessage(`{"name":"Invalid-1","credential":"plain","connection":"cred_wrong","application":"not-an-app"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 4 {
		t.Fatalf("expected 4 issues, got %#v", issues)
	}
}
