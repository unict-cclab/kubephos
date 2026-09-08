package plugins

import (
	"context"
	"encoding/json"
	"testing"
)

func TestResolveSecretsHonorsDeclaredKinds(t *testing.T) {
	process := &Process{
		secretKinds: map[string]bool{"allowed": true},
		resolver: func(context.Context, string) (string, json.RawMessage, error) {
			return "allowed", json.RawMessage(`{"value":"secret"}`), nil
		},
	}
	result, err := process.resolveSecrets(context.Background(), []byte(`{"credentialRef":"cred_one","message":"safe"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || string(result["cred_one"]) != `{"value":"secret"}` {
		t.Fatalf("unexpected resolved secrets: %#v", result)
	}
}

func TestResolveSecretsRejectsUndeclaredKinds(t *testing.T) {
	process := &Process{
		secretKinds: map[string]bool{"allowed": true},
		resolver: func(context.Context, string) (string, json.RawMessage, error) {
			return "forbidden", json.RawMessage(`{}`), nil
		},
	}
	if _, err := process.resolveSecrets(context.Background(), []byte(`{"credentialRef":"cred_one"}`)); err == nil {
		t.Fatal("expected permission error")
	}
}
