package resourceidentity

import (
	"encoding/json"
	"testing"
)

func TestVMIDScopeIsConnectionNotNode(t *testing.T) {
	a, _ := LedgerID("proxmox", json.RawMessage(`{"connectionRef":"conn_a","node":"pve1"}`), "qemu/100")
	same, _ := LedgerID("proxmox", json.RawMessage(`{"connectionRef":"conn_a","node":"pve2"}`), "qemu/100")
	b, _ := LedgerID("proxmox", json.RawMessage(`{"connectionRef":"conn_b","node":"pve1"}`), "qemu/100")
	if a != same || a == b || a != "conn_a/qemu/100" {
		t.Fatalf("incorrect scope: %s %s %s", a, same, b)
	}
	for _, raw := range []string{`{}`, `{"connectionRef":"a/b"}`, `{"connectionRef":null}`, `invalid`} {
		if _, err := LedgerID("proxmox", json.RawMessage(raw), "qemu/100"); err == nil {
			t.Fatalf("missing/invalid connection accepted: %s", raw)
		}
	}
	other, err := LedgerID("other", nil, "instance/id")
	if err != nil || other != "instance/id" {
		t.Fatal("changed other provider identity")
	}
}
