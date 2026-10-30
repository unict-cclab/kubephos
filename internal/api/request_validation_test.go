package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/schema"
)

func TestConfigurationValidationReturnsFieldLabelsAndStructuredPaths(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","properties":{"primaries":{"type":"integer","title":"Primary shards","minimum":3}}}`)
	issues, err := schema.Validate(definition, json.RawMessage(`{"primaries":1}`))
	if err != nil || len(issues) != 1 {
		t.Fatalf("expected one validation issue: %v %v", issues, err)
	}
	response := httptest.NewRecorder()
	experimentConfigurationIssues("applicationValues", definition, issues).write(response)
	var body struct {
		Validation struct {
			Issues []schema.Issue `json:"issues"`
		} `json:"validation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Validation.Issues) != 1 || body.Validation.Issues[0].Path != "applicationValues.primaries" || body.Validation.Issues[0].Message != "Primary shards: Value must be at least 3." {
		t.Fatalf("unexpected validation response: %s", response.Body.String())
	}
}

func TestDecodeJSONEnforcesOneBoundedObjectWithoutEchoingValues(t *testing.T) {
	for _, test := range []struct{ name, body, contains string }{
		{"empty", "", "JSON object"},
		{"null", "null", "JSON object"},
		{"array", "[]", "JSON object"},
		{"trailing object", `{"name":"lab"}{"name":"other"}`, "single JSON object"},
		{"syntax", `{"name":"secret-input"`, "invalid JSON"},
		{"field type", `{"name":{"secret-input":"value"}}`, "wrong type"},
		{"unknown field", `{"secret-input":"value"}`, "unsupported field"},
		{"oversized", `{"name":"lab"}` + strings.Repeat(" ", 1<<20) + `{}`, "too large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			var input struct {
				Name string `json:"name"`
			}
			err := decodeJSON(request, &input)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Contains(err.Error(), "secret-input") || strings.Contains(err.Error(), "unmarshal") {
				t.Fatal("request internals were exposed")
			}
		})
	}
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"lab"}`)), &input); err != nil || input.Name != "lab" {
		t.Fatalf("valid request rejected: %v", err)
	}
}
