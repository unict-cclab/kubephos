package engine

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestPersistedStepResultRedactsSensitiveOutputs(t *testing.T) {
	step := domain.PlanStep{Outputs: []domain.ArtifactOutput{{Name: "public"}, {Name: "cluster-connection", Sensitive: true}}}
	result, err := persistedStepResult(step, json.RawMessage(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"artifactOutputs":[{"name":"public","sensitive":false,"type":"","version":""},{"name":"cluster-connection","sensitive":true,"type":"","version":""}]}` {
		t.Fatalf("unexpected persisted result %s", result)
	}
}

func TestPersistedStepResultReplacesPublicArtifactPayloads(t *testing.T) {
	step := domain.PlanStep{Outputs: []domain.ArtifactOutput{{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1"}}}
	result, err := persistedStepResult(step, json.RawMessage(`{"manifestSet":{"content":"large"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"artifactOutputs":[{"name":"manifest-set","sensitive":false,"type":"ManifestSet","version":"v1alpha1"}]}` {
		t.Fatalf("unexpected persisted result %s", result)
	}
}

func TestPersistedStepResultKeepsPublicOutputs(t *testing.T) {
	raw := json.RawMessage(`{"healthy":true}`)
	result, err := persistedStepResult(domain.PlanStep{}, raw)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != string(raw) {
		t.Fatalf("unexpected persisted result %s", result)
	}
}
