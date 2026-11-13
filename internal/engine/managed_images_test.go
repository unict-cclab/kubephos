package engine

import (
	"context"
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestImageSyncUsesBootstrapIdentityAndSkipsCleanupAndMaintenance(t *testing.T) {
	step := domain.PlanStep{Input: []byte("{}"), ResolvedInputs: map[string]domain.ResolvedArtifact{"registry-endpoint": {Type: "RegistryEndpoint"}}}
	ctx := context.Background()
	if err := ensureBootstrapImages(ctx, "io.kubephos.cluster.k3s.bootstrap", step, nil); err == nil {
		t.Fatal("bootstrap did not validate publication identity")
	}
	if err := ensureBootstrapImages(ctx, "other", step, nil); err != nil {
		t.Fatal(err)
	}
	step.Cleanup = true
	if err := ensureBootstrapImages(ctx, "io.kubephos.cluster.k3s.bootstrap", step, nil); err != nil {
		t.Fatal("cleanup attempted publication")
	}
	step.Cleanup = false
	step.Input = []byte(`{"reconcile":true}`)
	if err := ensureBootstrapImages(ctx, "io.kubephos.cluster.k3s.bootstrap", step, nil); err != nil {
		t.Fatal("node maintenance attempted publication")
	}
}
