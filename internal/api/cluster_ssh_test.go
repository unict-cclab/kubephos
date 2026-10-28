package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
)

func TestClusterSSHKeyDownload(t *testing.T) {
	files := map[string][]byte{}
	filer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if value, ok := files[r.URL.Path]; ok {
			w.Write(value)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer filer.Close()
	server, pool := transferServer(t, filer.URL)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('pipe_ssh','ws_default','ssh','{}','{}','{}',repeat('a',64))`)
	exec(`INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('run_ssh','pipe_ssh','ws_default','ssh','succeeded',repeat('a',64),'MachineAccess','v1alpha1')`)
	exec(`INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster_ssh','ws_default','test','kubernetes-cluster','proxmox','test','v1','{}','pipe_ssh','run_ssh')`)
	keys := []string{}
	for i, stage := range []string{"machines", "c1-machines", "c2-machines"} {
		_, private, _ := ed25519.GenerateKey(rand.Reader)
		block, err := ssh.MarshalPrivateKey(private, "")
		if err != nil {
			t.Fatal(err)
		}
		key := string(pem.EncodeToMemory(block))
		keys = append(keys, key)
		raw, _ := json.Marshal(map[string]any{"kind": "MachineAccess", "spec": map[string]string{"privateKey": key}})
		artifactID, opID := fmt.Sprintf("art_ssh%d", i), fmt.Sprintf("op_ssh%d", i)
		nonce, encrypted, err := server.vault.EncryptBound(raw, []byte(artifactID))
		if err != nil {
			t.Fatal(err)
		}
		path := "/keys/" + artifactID
		files[path] = encrypted
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','ssh','succeeded','{}','{}','{}',repeat('a',64))`, opID)
		exec(`INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES($1,'run_ssh',$2,$3,'test','ssh','succeeded',$4)`, stage, i+1, stage, opID)
		_, err = server.store.CreateArtifact(ctx, domain.Artifact{ID: artifactID, OperationID: opID, Name: "machine-access", OutputName: "machine-access", Type: "MachineAccess", Version: "v1alpha1", MediaType: "application/json", StorageKey: path, Digest: artifacts.Digest(raw), SizeBytes: int64(len(raw)), StorageDigest: artifacts.Digest(encrypted), StoredSizeBytes: int64(len(encrypted)), EncryptionNonce: nonce, Sensitive: true, VerifiedAt: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
	}
	call := func(query string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/v1/kubernetes-clusters/cluster_ssh/ssh-key"+query, nil)
		r.SetPathValue("id", "cluster_ssh")
		w := httptest.NewRecorder()
		server.downloadClusterSSHKey(w, r)
		return w
	}
	check := func(query string, key string) {
		t.Helper()
		w := call(query)
		if w.Code != 200 || w.Body.String() != key {
			t.Fatalf("download failed: status %d", w.Code)
		}
		if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || !strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("missing private attachment headers")
		}
	}
	check("", keys[0])
	exec(`UPDATE managed_resources SET spec='{"mode":"multi","members":[{"name":"east"},{"name":"west"}]}' WHERE id='cluster_ssh'`)
	check("?cluster=east", keys[1])
	check("?cluster=west", keys[2])
	for _, query := range []string{"", "?cluster=other"} {
		if w := call(query); w.Code != 400 {
			t.Fatalf("invalid member accepted: %d", w.Code)
		}
	}
	exec(`UPDATE artifacts SET sensitive=false,encryption_nonce=NULL WHERE id='art_ssh2'`)
	if w := call("?cluster=west"); w.Code != 409 {
		t.Fatal("unprotected artifact accepted")
	}
	exec(`UPDATE pipeline_runs SET status='failed' WHERE id='run_ssh'`)
	if w := call("?cluster=east"); w.Code != 409 {
		t.Fatal("failed resource accepted")
	}
}

func TestClusterSSHKeyAccessPolicy(t *testing.T) {
	path := "/api/v1/kubernetes-clusters/cluster_ssh/ssh-key"
	if !adminReadPath(path) {
		t.Fatal("SSH key download must require administrator access")
	}
	action, _, target, ok := auditTarget(httptest.NewRequest("GET", path+"?cluster=east", nil))
	if !ok || action != "kubernetes-cluster.ssh-key.download" || target != "cluster_ssh" {
		t.Fatal("SSH key download must be audited")
	}
}
