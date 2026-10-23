package proxmoxvm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAllocationInventoryCoversAllNodesAndGuestTypes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=user@pve!inventory=secret" {
			t.Error("missing token authentication")
		}
		switch r.URL.Path {
		case "/api2/json/cluster/resources":
			fmt.Fprint(w, `{"data":[{"vmid":101,"node":"pve","type":"qemu","status":"running"},{"vmid":900,"node":"pve-2","type":"lxc"}]}`)
		case "/api2/json/nodes/pve/qemu/101/config":
			fmt.Fprint(w, `{"data":{"ipconfig0":"ip=10.20.0.10/24,gw=10.20.0.1"}}`)
		case "/api2/json/nodes/pve/qemu/101/agent/network-get-interfaces":
			fmt.Fprint(w, `{"data":{"result":[{"ip-addresses":[{"ip-address":"10.20.0.11"}]}]}}`)
		case "/api2/json/nodes/pve-2/lxc/900/config":
			fmt.Fprint(w, `{"data":{"net0":"ip=10.20.0.90/24,bridge=vmbr0"}}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	inventory, err := ReadAllocationInventory(context.Background(), server.URL, false, "user@pve!inventory", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 || inventory[1].VMID != 900 || inventory[1].Node != "pve-2" || len(inventory[0].Addresses) != 1 {
		t.Fatalf("incomplete inventory %+v", inventory)
	}
}
func TestAllocationInventoryFailsClosedOnUnreadableConfiguration(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			fmt.Fprint(w, `{"data":[{"vmid":101,"node":"pve","type":"qemu"}]}`)
		} else {
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	if _, err := ReadAllocationInventory(context.Background(), server.URL, false, "user@pve!inventory", "secret"); err == nil {
		t.Fatal("unreadable VM configuration accepted")
	}
}
