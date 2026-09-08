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

func TestResolveConnectionsHonorsProviderBoundary(t *testing.T) {
	process := &Process{
		manifest: Manifest{Provider: "proxmox"},
		connections: func(context.Context, string) (string, json.RawMessage, error) {
			return "proxmox", json.RawMessage(`{"endpoint":"https://pve.test"}`), nil
		},
	}
	result, err := process.resolveConnections(context.Background(), []byte(`{"connectionRef":"conn_one"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result["conn_one"]) != `{"endpoint":"https://pve.test"}` {
		t.Fatalf("unexpected connection: %#v", result)
	}
}

func TestResolveConnectionsRejectsAnotherProvider(t *testing.T) {
	process := &Process{
		manifest: Manifest{Provider: "proxmox"},
		connections: func(context.Context, string) (string, json.RawMessage, error) {
			return "vmware", json.RawMessage(`{}`), nil
		},
	}
	if _, err := process.resolveConnections(context.Background(), []byte(`{"connectionRef":"conn_one"}`)); err == nil {
		t.Fatal("expected provider boundary error")
	}
}
