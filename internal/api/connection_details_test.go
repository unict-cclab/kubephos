package api

import "testing"

func TestRedactConnectionInputs(t *testing.T) {
	value := map[string]any{"host": "192.0.2.1", "credentialRef": "cred_123", "password": "hidden", "nested": map[string]any{"apiToken": "secret", "zone": "east"}}
	result := redactConnectionInputs(value).(map[string]any)
	if result["host"] != "192.0.2.1" || result["credentialRef"] != "cred_123" || result["password"] != "••••••••" {
		t.Fatal(result)
	}
	nested := result["nested"].(map[string]any)
	if nested["apiToken"] != "••••••••" || nested["zone"] != "east" {
		t.Fatal(nested)
	}
}
