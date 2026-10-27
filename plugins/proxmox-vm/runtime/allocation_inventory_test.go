package proxmoxvm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAllocationInventoryCoversAllNodesAndGuestTypes(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "PVEAPIToken=user@pve!inventory=secret" {
			t.Error("missing token authentication")
		}
		switch r.URL.Path {
		case "/api2/json/nodes":
			fmt.Fprint(w, `{"data":[{"node":"pve","status":"online"},{"node":"pve-2","status":"online"}]}`)
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

func TestAllocationVMIDsIncludeOfflineNodesWithoutReadingConfigurations(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/cluster/resources" {
			t.Errorf("VMID checks must not read configs: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"data":[{"vmid":8016,"node":"minis02","type":"qemu","status":"unknown"},{"vmid":8101,"node":"minis03","type":"lxc","status":"stopped"}]}`)
	}))
	defer server.Close()
	vms, err := ReadAllocationVMIDs(context.Background(), server.URL, false, "user@pve!test", "secret")
	if err != nil || len(vms) != 2 || vms[0].VMID != 8016 || vms[1].VMID != 8101 {
		t.Fatalf("offline identifiers were lost: %+v %v", vms, err)
	}
}
func TestAllocationErrorsDescribeFailuresWithoutExposingResponseBodies(t *testing.T) {
	for _, test := range []struct {
		status         int
		body, expected string
	}{{403, "super-secret-response-body", "permission"}, {500, "hostname lookup 'minis02' failed super-secret-response-body", "hostname"}, {401, "super-secret-response-body", "token"}} {
		err := allocationReadError{vmid: 8016, node: "minis02", cause: apiError{Status: test.status, Message: test.body}}
		if !strings.Contains(err.Error(), "8016") || !strings.Contains(err.Error(), "minis02") || !strings.Contains(err.Error(), test.expected) || strings.Contains(err.Error(), "super-secret-response-body") {
			t.Fatalf("unsafe or uninformative allocation error: %s", err.Error())
		}
		if !errors.Is(err, ErrNetworkAllocationsUnavailable) {
			t.Fatal("missing partial network inventory classification")
		}
	}
}
func TestAllocationInventoryRejectsMissingInventoryData(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"data":null}`) }))
	defer server.Close()
	if _, err := ReadAllocationVMIDs(context.Background(), server.URL, false, "user@pve!test", "secret"); err == nil {
		t.Fatal("missing inventory accepted as empty cluster")
	}
}

func TestAllocationInventorySkipsOfflineNodesButKeepsTheirIdentifiers(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api2/json/cluster/resources":
			fmt.Fprint(w, `{"data":[{"vmid":101,"node":"offline","type":"qemu"},{"vmid":102,"node":"online","type":"lxc"}]}`)
		case "/api2/json/nodes":
			fmt.Fprint(w, `{"data":[{"node":"offline","status":"offline"},{"node":"online","status":"online"}]}`)
		case "/api2/json/nodes/online/lxc/102/config":
			fmt.Fprint(w, `{"data":{"net0":"ip=10.20.0.12/24,bridge=vmbr0"}}`)
		default:
			t.Errorf("offline node must not be queried: %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	guests, err := ReadAllocationInventory(context.Background(), server.URL, false, "user@pve!test", "secret")
	if err != nil || len(guests) != 2 || guests[0].VMID != 101 || guests[0].Config != nil || guests[1].Config["net0"] == nil {
		t.Fatalf("partial inventory lost identifiers or available network data: %+v %v", guests, err)
	}
}
