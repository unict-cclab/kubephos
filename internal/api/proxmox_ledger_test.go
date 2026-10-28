package api

import (
	"context"
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"testing"
)

func TestResourceEffectValidationUsesProxmoxConnectionScope(t *testing.T) {
	server, pool := transferServer(t, "")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO infrastructure_resources(id,provider,external_id,kind,name,state,ownership,protection) VALUES('protected','proxmox','conn_a/qemu/100','virtual-machine','existing','running','imported','read-only')`)
	if err != nil {
		t.Fatal(err)
	}
	manifest := plugins.Manifest{Provider: "proxmox"}
	step := func(connection, action string) domain.PlanStep {
		return domain.PlanStep{Input: json.RawMessage(`{"connectionRef":"` + connection + `"}`), Effects: []domain.ResourceEffect{{Action: action, ExternalID: "qemu/100"}}}
	}
	if err = server.validateResourceEffects(ctx, "ws_default", manifest, domain.Plan{Steps: []domain.PlanStep{step("conn_a", "create")}}); err == nil {
		t.Fatal("same-cluster protected VMID was accepted")
	}
	if err = server.validateResourceEffects(ctx, "ws_default", manifest, domain.Plan{Steps: []domain.PlanStep{step("conn_b", "create")}}); err != nil {
		t.Fatal(err)
	}
	// A prior create on B cannot authorize a deletion on A with the same local ID.
	if err = server.validateResourceEffects(ctx, "ws_default", manifest, domain.Plan{Steps: []domain.PlanStep{step("conn_b", "create"), step("conn_a", "delete")}}); err == nil {
		t.Fatal("cross-connection create authorized a protected deletion")
	}
}
