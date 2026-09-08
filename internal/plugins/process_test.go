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

func TestResolveCatalogRequiresPermission(t *testing.T) {
	process := &Process{
		catalog: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"kind":"Application"}`), nil
		},
	}
	if _, err := process.resolveCatalog(context.Background(), []byte(`{"applicationRef":"app:dev.example.app@1.0.0"}`)); err == nil {
		t.Fatal("expected catalog permission error")
	}
	process.manifest.Permissions = []string{"catalog.read:applications"}
	result, err := process.resolveCatalog(context.Background(), []byte(`{"applicationRef":"app:dev.example.app@1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("unexpected catalog result: %#v", result)
	}
}

func TestReferenceResolutionExcludesVerifiedArtifactContents(t *testing.T) {
	payload, err := referenceResolutionPayload([]byte(`{"step":{"input":{"machineSetRef":"art_one"},"resolvedInputs":{"machines":{"value":{"connectionRef":"conn_provider"}},"access":{"value":{"credentialRef":"cred_private"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	connectionRefs := map[string]bool{}
	credentialRefs := map[string]bool{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	collectRefs(value, "conn_", connectionRefs)
	collectRefs(value, "cred_", credentialRefs)
	if len(connectionRefs) != 0 || len(credentialRefs) != 0 {
		t.Fatalf("artifact values leaked into reference resolution: %s", payload)
	}
}

func TestReferenceResolutionExcludesPluginResult(t *testing.T) {
	payload, err := referenceResolutionPayload([]byte(`{"step":{"input":{"applicationRef":"app:dev.example.input@1.0.0"}},"result":{"applicationRef":"app:dev.example.output@1.0.0","credentialRef":"cred_output"}}`))
	if err != nil {
		t.Fatal(err)
	}
	applicationRefs := map[string]bool{}
	credentialRefs := map[string]bool{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	collectRefs(value, "app:", applicationRefs)
	collectRefs(value, "cred_", credentialRefs)
	if !applicationRefs["app:dev.example.input@1.0.0"] || applicationRefs["app:dev.example.output@1.0.0"] || len(credentialRefs) != 0 {
		t.Fatalf("plugin result leaked into reference resolution: %s", payload)
	}
}
