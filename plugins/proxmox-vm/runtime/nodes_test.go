package proxmoxvm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadProxmoxNodesIncludesOfflineNodesWithoutInspectingGuests(t *testing.T) {
	calls := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api2/json/nodes" {
			t.Errorf("unexpected guest lookup: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "PVEAPIToken=user@pve!nodes=secret" {
			t.Error("missing authentication")
		}
		fmt.Fprint(w, `{"data":[{"node":"pve-2","status":"offline"},{"node":"pve","status":"online"}]}`)
	}))
	defer server.Close()
	nodes, err := ReadProxmoxNodes(context.Background(), server.URL, false, "user@pve!nodes", "secret")
	if err != nil || len(nodes) != 2 || nodes[0].Name != "pve" || !nodes[0].Online || nodes[1].Online || calls != 1 {
		t.Fatalf("unexpected nodes: %#v %v", nodes, err)
	}
}

func TestReadProxmoxNodesDoesNotExposeRemoteCredentials(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		fmt.Fprint(w, "private Proxmox error token=secret")
	}))
	defer server.Close()
	_, err := ReadProxmoxNodes(context.Background(), server.URL, false, "user@pve!nodes", "secret")
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("remote error was not sanitized")
	}
}
