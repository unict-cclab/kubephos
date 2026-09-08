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
