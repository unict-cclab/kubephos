package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/provisioning"
)

func TestProvisioningWithAnOfflineUnrelatedNode(t *testing.T) {
	var configReads atomic.Int32
	proxmox := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/cluster/resources" {
			fmt.Fprint(w, `{"data":[{"vmid":101,"node":"minis02","type":"qemu","status":"unknown"}]}`)
			return
		}
		configReads.Add(1)
		w.WriteHeader(500)
		fmt.Fprint(w, `{"message":"hostname lookup 'minis02' failed sensitive-response-body"}`)
	}))
	defer proxmox.Close()
	server, pool := transferServer(t, "")
	ctx := context.Background()
	nonce, ciphertext, fingerprint, err := server.vault.Encrypt([]byte(`{"tokenId":"user@pve!test","tokenSecret":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.store.CreateCredential(ctx, domain.EncryptedCredential{Credential: domain.Credential{ID: "cred-proxmox", Name: "token", Kind: "proxmox-api-token", Fingerprint: fingerprint}, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	config, _ := json.Marshal(map[string]any{"endpoint": proxmox.URL, "credentialRef": "cred-proxmox", "verifyTLS": false})
	connection, err := server.store.CreateProviderConnection(ctx, domain.ProviderConnection{ID: "connection", Name: "Proxmox", Provider: "proxmox", PluginID: "proxmox", Configuration: config})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/machine-templates", nil)
	w := httptest.NewRecorder()
	release, ok := server.validateProvisioning(w, request, connection.ID, map[string]any{"vmid": float64(102)}, 1)
	if !ok {
		t.Fatalf("DHCP creation blocked by unrelated offline node: %s", w.Body.String())
	}
	release()
	if configReads.Load() != 0 {
		t.Fatal("DHCP/VMID validation requested network configurations")
	}
	w = httptest.NewRecorder()
	_, ok = server.validateProvisioning(w, request, connection.ID, map[string]any{"vmid": float64(101)}, 1)
	if ok || !strings.Contains(w.Body.String(), "already in use") {
		t.Fatal("offline VMID conflict was not rejected")
	}
	// An unavailable unrelated node must not block static addressing either.
	w = httptest.NewRecorder()
	release, ok = server.validateProvisioning(w, request, connection.ID, map[string]any{"vmid": float64(102), "address": "10.20.0.20", "prefixLength": float64(24), "gateway": "10.20.0.1", "dnsServer": "1.1.1.1"}, 1)
	if !ok {
		t.Fatal("unavailable node blocked provisioning", w.Body.String())
	}
	release()
	// Offline guests still reserve VMIDs; no network is invented without a profile.
	request = httptest.NewRequest(http.MethodGet, "/api/v1/connections/connection/provisioning-suggestions", nil)
	request.SetPathValue("id", connection.ID)
	w = httptest.NewRecorder()
	server.getProvisioningSuggestions(w, request)
	var suggestion provisioning.Allocation
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &suggestion) != nil || suggestion.VMID != 102 || suggestion.Address != "" {
		t.Fatalf("unexpected partial suggestion: %d %s", w.Code, w.Body.String())
	}
	// Saved reservations remain enforced even when the live node is offline.
	_, err = pool.Exec(ctx, `INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES('saved-operation','ws_default','test','Template','succeeded','{}','{}','{}',repeat('a',64))`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO managed_resources(id,workspace_id,name,kind,provider,connection_id,plugin_id,plugin_version,spec,operation_id) VALUES('saved-template','ws_default','reserved','machine-template','proxmox',$1,'test','v1','{"vmid":202,"address":"10.20.0.20","prefixLength":24,"gateway":"10.20.0.1","dnsServer":"1.1.1.1"}','saved-operation')`, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	_, ok = server.validateProvisioning(w, request, connection.ID, map[string]any{"vmid": float64(102), "address": "10.20.0.20", "prefixLength": float64(24), "gateway": "10.20.0.1", "dnsServer": "1.1.1.1"}, 1)
	if ok || !strings.Contains(w.Body.String(), "already in use or reserved") {
		t.Fatal("saved IP reservation was bypassed", w.Body.String())
	}
}
