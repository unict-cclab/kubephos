package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/testutil"
	"kubephos.dev/kubephos/internal/transfer"
)

func transferServer(t *testing.T, filer string) (*Server, *pgxpool.Pool) {
	t.Helper()
	uri := testutil.Database(t)
	ctx := context.Background()
	store, err := storage.Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	vault, err := secrets.Open(filepath.Join(t.TempDir(), "master.key"))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{store: store, vault: vault, artifacts: artifacts.New(filer), version: "transfer-test"}, pool
}
func transferCall(t *testing.T, handler http.HandlerFunc, path string, body io.Reader, contentType string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, body)
	r.Header.Set("Content-Type", contentType)
	r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: "destination-admin", Role: "admin"}))
	w := httptest.NewRecorder()
	handler(w, r)
	return w
}
func importCall(t *testing.T, handler http.HandlerFunc, archive []byte, password string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, _ := writer.CreateFormFile("file", "state.kps")
	_, _ = file.Write(archive)
	_ = writer.WriteField("password", password)
	_ = writer.Close()
	return transferCall(t, handler, "/api/v1/administration/state/import", &body, writer.FormDataContentType())
}
func TestStateTransferAcrossVaultsAndIsolatedInstances(t *testing.T) {
	var mu sync.Mutex
	files := map[string][]byte{}
	filer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "DELETE" {
			for key := range files {
				if key == r.URL.Path || strings.HasPrefix(key, r.URL.Path+"/") {
					delete(files, key)
				}
			}
			w.WriteHeader(204)
			return
		}
		if r.Method == "POST" {
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			defer file.Close()
			files[r.URL.Path], _ = io.ReadAll(file)
			w.WriteHeader(201)
			return
		}
		value, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		_, _ = w.Write(value)
	}))
	defer filer.Close()
	source, pool := transferServer(t, filer.URL)
	destination, target := transferServer(t, filer.URL)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role,external_id) VALUES ('source-admin','source','admin','keycloak-source'),('source-other','other','user','keycloak-other')`)
	if _, err := target.Exec(ctx, `INSERT INTO users(id,username,role,external_id) VALUES ('destination-admin','destination','admin','keycloak-destination')`); err != nil {
		t.Fatal(err)
	}
	op := "op_" + strings.Repeat("a", 24)
	run := "run_" + strings.Repeat("a", 24)
	pipeline := "pipe_" + strings.Repeat("a", 24)
	artifactID := "art_" + strings.Repeat("a", 24)
	exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES ($1,'ws_default','test','Infrastructure','succeeded','{}','{}','{}',repeat('a',64))`, op)
	nonce, ciphertext, fingerprint, err := source.vault.Encrypt([]byte(`{"token":"source-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.store.CreateCredential(ctx, domain.EncryptedCredential{Credential: domain.Credential{ID: "cred_source", Name: "proxmox", Kind: "proxmox", Fingerprint: fingerprint}, Nonce: nonce, Ciphertext: ciphertext}); err != nil {
		t.Fatal(err)
	}
	plain := []byte(`{"kubeconfig":"private-cluster-connection"}`)
	artNonce, encrypted, err := source.vault.EncryptBound(plain, []byte(artifactID))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := source.artifacts.Put(ctx, "/kubephos/operations/"+op+"/connection", "connection", "application/json", encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = source.store.CreateArtifact(ctx, domain.Artifact{ID: artifactID, OperationID: op, Name: "connection", Type: "ClusterConnection", Version: "v1alpha1", MediaType: "application/json", StorageKey: stored.Key, Digest: artifacts.Digest(plain), SizeBytes: int64(len(plain)), StorageDigest: stored.Digest, StoredSizeBytes: stored.Size, EncryptionNonce: artNonce, Sensitive: true}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES($1,'ws_default','cluster','{}','{}','{}',repeat('a',64))`, pipeline)
	exec(`INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version,result_artifact_id,owner_user_id) VALUES($1,$2,'ws_default','Provision cluster','succeeded',repeat('a',64),'ClusterConnection','v1alpha1',$3,'source-admin')`, run, pipeline, artifactID)
	exec(`INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES('stage',$1,1,'provision','test','Provision','succeeded',$2)`, run, op)
	exec(`INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','cluster','kubernetes-cluster','proxmox','test','v1','{}',$1,$2)`, pipeline, run)
	// Exercise the run/managed-resource FK cycle.
	exec(`UPDATE pipeline_runs SET cluster_resource_id='cluster' WHERE id=$1`, run)
	exec(`INSERT INTO experiment_configurations(id,workspace_id,cluster_resource_id,name,application_ref,application_digest,definition,validation,owner_user_id,shared_with_everyone) VALUES ('config-a','ws_default','cluster','same-name','app:test','sha256:test','{}','{}','source-admin',true),('config-b','ws_default','cluster','same-name','app:test','sha256:test','{}','{}','source-other',false)`)
	exec(`INSERT INTO experiment_configurations(id,workspace_id,name,application_ref,application_digest,definition,validation,owner_user_id) VALUES ('suite','ws_default','suite','','','{"kind":"suite","variants":[{"configurationId":"config-a"},{"configurationId":"config-b"}]}','{}','source-admin')`)
	exec(`INSERT INTO audit_events(actor,action,target_type,target_id,outcome,details) VALUES('source','configuration.create','configuration','config-a','succeeded','{"method":"POST","path":"/api/v1/experiment-configurations","status":201}')`)
	exec(`INSERT INTO configuration_shares VALUES('config-a','source-other')`)
	exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id,configuration_id) VALUES('old-experiment','ws_default','exclude-me','succeeded','TimeSeriesDataset','v1alpha1','source-admin','config-a')`)
	// Unrelated historical operations must not travel with the infrastructure.
	exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES('op_bbbbbbbbbbbbbbbbbbbbbbbb','ws_default','test','exclude-me','succeeded','{}','{}','{}',repeat('b',64))`)
	// Preserve standalone VM provenance as well, while excluding experiment
	// artifacts and ledger entries even when they share the same workspace.
	ledgerOp := "op_" + strings.Repeat("c", 24)
	exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','VM maintenance','succeeded','{}','{}','{}',repeat('c',64))`, ledgerOp)
	exec(`INSERT INTO infrastructure_resources(id,provider,external_id,workspace_id,kind,name,state,ownership,protection,created_by_operation_id) VALUES('vm','proxmox','qemu/100','ws_default','virtual-machine','node','running','managed','managed',$1)`, ledgerOp)
	experimentArtifact := "art_" + strings.Repeat("b", 24)
	data := []byte(`{"experiment":"exclude-this-dataset"}`)
	result, e := source.artifacts.Put(ctx, "/kubephos/operations/op_bbbbbbbbbbbbbbbbbbbbbbbb/result", "dataset", "application/json", data)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = source.store.CreateArtifact(ctx, domain.Artifact{ID: experimentArtifact, OperationID: "op_bbbbbbbbbbbbbbbbbbbbbbbb", Name: "result", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", StorageKey: result.Key, Digest: result.Digest, SizeBytes: result.Size}); e != nil {
		t.Fatal(e)
	}
	exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias) VALUES('variant','old-experiment',1,'variant','variant')`)
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,operation_id,result_artifact_id,completed_at) VALUES('trial','variant',1,'succeeded','op_bbbbbbbbbbbbbbbbbbbbbbbb',$1,now())`, experimentArtifact)
	exec(`INSERT INTO infrastructure_resources(id,provider,external_id,workspace_id,kind,name,state,ownership,protection,created_by_operation_id) VALUES('experiment-resource','kubernetes','pod/test','ws_default','pod','application-pod','ready','managed','managed','op_bbbbbbbbbbbbbbbbbbbbbbbb')`)
	password := "portable-archive-password"
	export := transferCall(t, source.exportState, "/api/v1/administration/state/export", strings.NewReader(`{"password":"`+password+`"}`), "application/json")
	if export.Code != 200 {
		t.Fatalf("export: %d %s", export.Code, export.Body.String())
	}
	archive := bytes.Clone(export.Body.Bytes())
	state, err := transfer.Decrypt(archive, password)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tables["infrastructure_resources"]) != 1 || len(state.Tables["operations"]) != 2 || len(state.Tables["pipeline_runs"]) != 1 || len(state.Tables["artifacts"]) != 1 {
		t.Fatal("infrastructure provenance not preserved or unrelated history included")
	}
	for _, forbidden := range []string{"experiments", "users", "configuration_shares", "auth_sessions"} {
		if _, ok := state.Tables[forbidden]; ok {
			t.Fatal("excluded data included", forbidden)
		}
	}
	inspect := importCall(t, destination.inspectState, archive, password)
	if inspect.Code != 200 || bytes.Contains(inspect.Body.Bytes(), []byte("source-secret")) {
		t.Fatalf("inspect failed or secret leaked: %d", inspect.Code)
	}
	mismatch, e := transfer.Decrypt(archive, password)
	if e != nil {
		t.Fatal(e)
	}
	mismatch.Schema = "different-schema"
	incompatible, e := transfer.Encrypt(mismatch, password)
	if e != nil {
		t.Fatal(e)
	}
	incompatibleResult := importCall(t, destination.inspectState, incompatible, password)
	if incompatibleResult.Code != 422 || !strings.Contains(incompatibleResult.Body.String(), "schema") {
		t.Fatal("incompatible schema accepted")
	}
	wrong := importCall(t, destination.importState, archive, "incorrect-password")
	if wrong.Code < 400 {
		t.Fatal("wrong password accepted")
	}
	imported := importCall(t, destination.importState, archive, password)
	if imported.Code != 200 {
		t.Fatalf("import: %d %s", imported.Code, imported.Body.String())
	}
	attribution, e := destination.store.ResourceAttributions(ctx, "/api/v1/experiment-configurations", []string{"config-a"})
	if e != nil || attribution["config-a"].CreatedBy != "source" {
		t.Fatal("resource authorship not retained", e)
	}
	// Destination's preexisting identity survives; source identities/shares/history do not.
	var valid bool
	err = target.QueryRow(ctx, `SELECT (SELECT count(*) FROM users)=1 AND EXISTS(SELECT 1 FROM users WHERE id='destination-admin') AND NOT EXISTS(SELECT 1 FROM experiments) AND NOT EXISTS(SELECT 1 FROM configuration_shares) AND (SELECT count(*) FROM experiment_configurations)=3 AND NOT EXISTS(SELECT 1 FROM experiment_configurations WHERE owner_user_id<>'destination-admin' OR shared_with_everyone) AND (SELECT count(*) FROM configuration_suite_members)=2 AND NOT EXISTS(SELECT 1 FROM artifact_deletions) AND EXISTS(SELECT 1 FROM pipeline_runs WHERE cluster_resource_id='cluster')`).Scan(&valid)
	if err != nil || !valid {
		t.Fatal("destination state/ownership mismatch", err)
	}
	var uniqueNames int
	if err = target.QueryRow(ctx, `SELECT count(DISTINCT name) FROM experiment_configurations`).Scan(&uniqueNames); err != nil || uniqueNames != 3 {
		t.Fatal("configuration names not disambiguated", err)
	}
	var targetNonce, targetCipher []byte
	if err = target.QueryRow(ctx, `SELECT nonce,ciphertext FROM credentials WHERE id='cred_source'`).Scan(&targetNonce, &targetCipher); err != nil {
		t.Fatal(err)
	}
	decrypted, err := destination.vault.Decrypt(targetNonce, targetCipher)
	if err != nil || string(decrypted) != `{"token":"source-secret"}` || bytes.Equal(targetCipher, ciphertext) {
		t.Fatal("credential was not re-encrypted", err)
	}
	a, err := destination.store.GetArtifact(ctx, artifactID)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err = destination.readArtifactPayload(ctx, a)
	if err != nil || !bytes.Equal(decrypted, plain) || a.StorageKey == stored.Key {
		t.Fatal("sensitive artifact not migrated", err)
	}
	repeated := importCall(t, destination.importState, archive, password)
	if repeated.Code != 409 {
		t.Fatal("populated destination accepted a second import", repeated.Code)
	}
	// A late foreign-key failure rolls back every metadata table. Object cleanup
	// remains durable and never targets an existing source/destination object.
	rollbackServer, rollbackPool := transferServer(t, filer.URL)
	_, _ = rollbackPool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES ('destination-admin','admin','admin')`)
	invalidState, e := transfer.Decrypt(archive, password)
	if e != nil {
		t.Fatal(e)
	}
	transfer.Set(invalidState.Tables["experiment_configurations"][0], "cluster_resource_id", "missing-cluster")
	invalidArchive, e := transfer.Encrypt(invalidState, password)
	if e != nil {
		t.Fatal(e)
	}
	failed := importCall(t, rollbackServer.importState, invalidArchive, password)
	if failed.Code < 400 || strings.Contains(failed.Body.String(), "missing-cluster") || strings.Contains(failed.Body.String(), "source-secret") {
		t.Fatal("invalid import accepted or database details exposed")
	}
	var rolledBack bool
	e = rollbackPool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM managed_resources) AND NOT EXISTS(SELECT 1 FROM credentials) AND NOT EXISTS(SELECT 1 FROM operations) AND EXISTS(SELECT 1 FROM artifact_deletions WHERE retry_after <= now())`).Scan(&rolledBack)
	if e != nil || !rolledBack {
		t.Fatal("late failure did not roll back state and schedule object cleanup", e)
	}
	if e = rollbackServer.store.ProcessArtifactDeletions(ctx, rollbackServer.artifacts.Delete); e != nil {
		t.Fatal(e)
	}
	var pending int
	_ = rollbackPool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions`).Scan(&pending)
	if pending != 0 {
		t.Fatal("failed import objects were not cleaned")
	}
	retained, e := destination.readArtifactPayload(ctx, a)
	if e != nil || !bytes.Equal(retained, plain) {
		t.Fatal("cleanup removed a valid destination object", e)
	}
	// Even a valid password-protected archive cannot queue jobs on the destination.
	active := state.Tables["operations"][0]
	transfer.Set(active, "status", "queued")
	fresh, freshPool := transferServer(t, filer.URL)
	_, _ = freshPool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES ('destination-admin','admin','admin')`)
	malicious, err := transfer.Encrypt(state, password)
	if err != nil {
		t.Fatal(err)
	}
	rejected := importCall(t, fresh.importState, malicious, password)
	if rejected.Code < 400 {
		t.Fatal("unfinished archive operation accepted")
	}
	var count int
	_ = freshPool.QueryRow(ctx, `SELECT count(*) FROM managed_resources`).Scan(&count)
	if count != 0 {
		t.Fatal("invalid archive left partial metadata")
	}
	// Export is unavailable while source work is queued.
	exec(`UPDATE operations SET status='queued' WHERE id=$1`, op)
	busy := transferCall(t, source.exportState, "/api/v1/administration/state/export", strings.NewReader(`{"password":"`+password+`"}`), "application/json")
	if busy.Code != 409 {
		t.Fatal("export allowed active work", busy.Code)
	}
}

func TestStateTransferRequiresAdministratorAndCSRF(t *testing.T) {
	server, pool := transferServer(t, "")
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES('regular','regular','user'),('admin','admin','admin')`)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"regular", "admin"} {
		token := role + strings.Repeat("-session", 6)
		if err = server.store.CreateSession(ctx, domain.Session{TokenHash: tokenHash(token), CSRFToken: "csrf", User: domain.User{ID: role}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	called := false
	handler := server.authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
	for _, test := range []struct {
		token, csrf string
		status      int
	}{{"", "", 401}, {"regular" + strings.Repeat("-session", 6), "csrf", 403}, {"admin" + strings.Repeat("-session", 6), "", 403}, {"admin" + strings.Repeat("-session", 6), "csrf", 204}} {
		called = false
		r := httptest.NewRequest("POST", "/api/v1/administration/state/export", nil)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: test.token})
		r.Header.Set("X-CSRF-Token", test.csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != test.status || called != (test.status == 204) {
			t.Fatalf("authorization: want %d got %d", test.status, w.Code)
		}
	}
}

func TestStateImportFailureDoesNotExposeDatabaseDetails(t *testing.T) {
	w := httptest.NewRecorder()
	transferError(w, io.ErrUnexpectedEOF)
	var value map[string]any
	if json.Unmarshal(w.Body.Bytes(), &value) != nil || strings.Contains(w.Body.String(), "EOF") {
		t.Fatal("internal transfer details exposed")
	}
}
