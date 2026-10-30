package schema

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssueMessageUsesSchemaLabelsWithoutJSONPaths(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","properties":{"primaries":{"type":"integer","title":"Primary shards"},"services":{"type":"array","title":"Services","items":{"type":"object","properties":{"cpuRequest":{"title":"CPU request"}}}}}}`)
	for _, test := range []struct{ path, label string }{{"$.primaries", "Primary shards"}, {"$.services[1].cpuRequest", "Services · Item 2 · CPU request"}, {"$.missingField", "Missing Field"}, {"$", ""}} {
		message := IssueMessage(definition, Issue{Path: test.path, Message: "Value must be at least 3."})
		if !strings.Contains(message, test.label) || strings.Contains(message, "$.") {
			t.Fatalf("unexpected user message: %s", message)
		}
	}
}

func TestDescribeIssuesPreservesPathsAndOriginalMessages(t *testing.T) {
	issues := []Issue{{Path: "$.name", Message: "Value is required."}}
	described := DescribeIssues(json.RawMessage(`{"properties":{"name":{"title":"Name"}}}`), issues)
	if described[0].Path != issues[0].Path || described[0].Message != "Name: Value is required." || issues[0].Message != "Value is required." {
		t.Fatalf("unexpected issue conversion: %v, original: %v", described, issues)
	}
}
func TestValidationOrderAndTrailingJSON(t *testing.T) {
	definition := json.RawMessage(`{"type":"object","required":["name","count"],"properties":{"name":{"type":"string"},"count":{"type":"integer"}}}`)
	for i := 0; i < 10; i++ {
		issues, err := Validate(definition, json.RawMessage(`{}`))
		if err != nil || len(issues) != 2 || issues[0].Path != "$.count" || issues[1].Path != "$.name" {
			t.Fatalf("validation order changed: %v %v", issues, err)
		}
	}
	issues, err := Validate(definition, json.RawMessage(`{} {}`))
	if err != nil || len(issues) != 1 || !strings.Contains(issues[0].Message, "one valid JSON") {
		t.Fatalf("trailing JSON accepted: %v %v", issues, err)
	}
}
