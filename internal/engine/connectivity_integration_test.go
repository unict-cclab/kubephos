package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/testutil"
)

func TestConnectivityMonitorPersistsServiceFailureAndRecovery(t *testing.T) {
	ctx := context.Background()
	database := testutil.Database(t)
	store, err := storage.Open(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	pool, err := pgxpool.New(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, fingerprint, err := vault.Encrypt([]byte(`{"tokenId":"test@pam!monitor","tokenSecret":"test-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	credential, err := store.CreateCredential(ctx, domain.EncryptedCredential{Credential: domain.Credential{ID: "cred_monitor", Name: "monitor", Kind: "proxmox-api-token", Fingerprint: fingerprint}, Nonce: nonce, Ciphertext: ciphertext})
	if err != nil {
		t.Fatal(err)
	}
	var status atomic.Int32
	status.Store(200)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api2/json/nodes" || r.Header.Get("Authorization") != "PVEAPIToken=test@pam!monitor=test-secret" {
			t.Error("monitor did not use the saved connection")
		}
		w.WriteHeader(int(status.Load()))
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	raw, _ := json.Marshal(map[string]any{"endpoint": server.URL, "credentialRef": credential.ID, "verifyTLS": true})
	original, err := store.CreateProviderConnection(ctx, domain.ProviderConnection{ID: "conn_monitor", Name: "Proxmox", Provider: "proxmox", PluginID: "test", Configuration: raw})
	if err != nil {
		t.Fatal(err)
	}
	worker := &Worker{store: store, vault: vault}
	for _, step := range []struct {
		code int
		want string
	}{{200, "reachable"}, {503, "checking"}, {503, "unavailable"}, {200, "reachable"}} {
		status.Store(int32(step.code))
		if _, err = pool.Exec(ctx, `UPDATE provider_connections SET connectivity_next_check_at=now()-interval '1 second' WHERE id=$1`, original.ID); err != nil {
			t.Fatal(err)
		}
		worker.checkConnectivity(ctx)
		actual, err := store.GetProviderConnectionConfiguration(ctx, original.ID)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Connectivity == nil || actual.Connectivity.Status != step.want || !actual.UpdatedAt.Equal(original.UpdatedAt) {
			t.Fatalf("unexpected observed state or attribution: %+v", actual.Connectivity)
		}
		value, _ := json.Marshal(actual.Connectivity)
		if strings.Contains(string(value), "test-secret") || strings.Contains(string(value), server.URL) {
			t.Fatal("private connection details entered the monitoring report")
		}
	}
}
