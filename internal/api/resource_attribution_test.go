package api

import (
	"encoding/json"
	"testing"
)

func TestAttributionOnlyIncludesResourceResponses(t *testing.T) {
	for _, path := range []string{"/api/v1/users", "/api/v1/catalog/models", "/api/v1/connections/conn"} {
		if attributionBase(path) == "" {
			t.Fatalf("missing resource: %s", path)
		}
	}
	for _, path := range []string{"/api/v1/connections/conn/secret", "/api/v1/operations/op/logs", "/api/v1/audit", "/api/v1/auth/status"} {
		if attributionBase(path) != "" {
			t.Fatalf("unexpected resource: %s", path)
		}
	}
	var body any
	_ = json.Unmarshal([]byte(`{"items":[{"id":"alice"}],"assignments":[{"id":"assignment"}]}`), &body)
	items := attributionItems(body)
	if len(items) != 1 || items[0]["id"] != "alice" {
		t.Fatalf("unexpected resources: %+v", items)
	}
}
