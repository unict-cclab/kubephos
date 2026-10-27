package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/transfer"
)

func bytea(row transfer.Row, key string) ([]byte, error) {
	value := transfer.String(row, key)
	if !strings.HasPrefix(value, `\x`) {
		return nil, errors.New("invalid encrypted credential metadata")
	}
	return hex.DecodeString(value[2:])
}
func setBytea(row transfer.Row, key string, value []byte) {
	transfer.Set(row, key, `\x`+hex.EncodeToString(value))
}
func artifactRow(row transfer.Row) (domain.Artifact, error) {
	a := domain.Artifact{ID: transfer.String(row, "id"), OperationID: transfer.String(row, "operation_id"), Name: transfer.String(row, "name"), MediaType: transfer.String(row, "media_type"), StorageKey: transfer.String(row, "storage_key"), Digest: transfer.String(row, "digest"), StorageDigest: transfer.String(row, "storage_digest")}
	if err := json.Unmarshal(row["size_bytes"], &a.SizeBytes); err != nil {
		return a, err
	}
	if err := json.Unmarshal(row["stored_size_bytes"], &a.StoredSizeBytes); err != nil {
		return a, err
	}
	if err := json.Unmarshal(row["sensitive"], &a.Sensitive); err != nil {
		return a, err
	}
	if a.Sensitive {
		var err error
		a.EncryptionNonce, err = bytea(row, "encryption_nonce")
		if err != nil || len(a.EncryptionNonce) != 12 {
			return a, errors.New("invalid encrypted artifact metadata")
		}
	}
	return a, nil
}
func transferError(response http.ResponseWriter, err error) {
	status := http.StatusUnprocessableEntity
	if errors.Is(err, storage.ErrTransferBusy) || errors.Is(err, storage.ErrTransferTarget) {
		status = http.StatusConflict
	}
	// Database/storage failures can include secret JSON values in DETAIL. Only
	// known, actionable errors may reach clients or logs.
	message := "Could not transfer instance state. Check that the archive is valid and the destination is a fresh installation."
	for _, known := range []error{storage.ErrTransferBusy, storage.ErrTransferTarget, storage.ErrTransferSchema, transfer.ErrArchive} {
		if errors.Is(err, known) {
			message = known.Error()
		}
	}
	writeError(response, status, "state_transfer_failed", message)
}
func (s *Server) beginStateTransfer(response http.ResponseWriter) bool {
	response.Header().Set("Cache-Control", "no-store")
	if !s.transferMu.TryLock() {
		writeError(response, http.StatusConflict, "state_transfer_busy", "Another state transfer is in progress.")
		return false
	}
	return true
}
func (s *Server) exportState(response http.ResponseWriter, request *http.Request) {
	if !s.beginStateTransfer(response) {
		return
	}
	defer s.transferMu.Unlock()
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, 400, "invalid_request", "Provide an archive password.")
		return
	}
	if len(input.Password) < 12 || len(input.Password) > 1024 {
		writeError(response, 422, "invalid_password", "Use an archive password between 12 and 1024 characters.")
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Minute)
	defer cancel()
	state, err := s.store.ExportState(ctx)
	if err != nil {
		transferError(response, err)
		return
	}
	state.Version = s.version
	for _, row := range state.Tables["credentials"] {
		nonce, e := bytea(row, "nonce")
		if e != nil || len(nonce) != 12 {
			transferError(response, errors.New("invalid credential nonce"))
			return
		}
		ciphertext, e := bytea(row, "ciphertext")
		if e != nil {
			transferError(response, e)
			return
		}
		plain, e := s.vault.Decrypt(nonce, ciphertext)
		if e != nil {
			transferError(response, e)
			return
		}
		state.Credentials[transfer.String(row, "id")] = plain
		transfer.Set(row, "nonce", nil)
		transfer.Set(row, "ciphertext", nil)
	}
	total := 0
	for _, row := range state.Tables["artifacts"] {
		a, e := artifactRow(row)
		if e != nil {
			transferError(response, e)
			return
		}
		plain, e := s.readArtifactPayload(ctx, a)
		if e != nil {
			transferError(response, e)
			return
		}
		total += len(plain)
		if total > transfer.MaxBytes/2 {
			writeError(response, 413, "archive_too_large", "Infrastructure artifacts exceed the state archive size limit.")
			return
		}
		state.Objects[a.ID] = plain
		transfer.Set(row, "encryption_nonce", nil)
	}
	value, err := transfer.Encrypt(state, input.Password)
	if err != nil {
		transferError(response, err)
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="kubephos-state-%s.kps"`, state.CreatedAt.Format("20060102-150405")))
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = response.Write(value)
}
func (s *Server) readStateArchive(response http.ResponseWriter, request *http.Request) (*transfer.State, bool) {
	request.Body = http.MaxBytesReader(response, request.Body, transfer.MaxBytes+(1<<20))
	if err := request.ParseMultipartForm(1 << 20); err != nil {
		writeError(response, 400, "invalid_archive", "Choose an encrypted state archive smaller than 128 MiB.")
		return nil, false
	}
	defer request.MultipartForm.RemoveAll()
	file, _, err := request.FormFile("file")
	if err != nil {
		writeError(response, 400, "invalid_archive", "Choose a state archive.")
		return nil, false
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, transfer.MaxBytes+1))
	if err != nil || len(value) > transfer.MaxBytes {
		writeError(response, 413, "archive_too_large", "State archive exceeds 128 MiB.")
		return nil, false
	}
	state, err := transfer.Decrypt(value, request.FormValue("password"))
	if err != nil {
		transferError(response, err)
		return nil, false
	}
	if err = s.store.CheckStateImport(request.Context(), state); err != nil {
		transferError(response, err)
		return nil, false
	}
	return state, true
}
func stateSummary(state *transfer.State) map[string]any {
	counts := map[string]int{}
	for _, table := range []string{"credentials", "provider_connections", "managed_resources", "experiment_configurations", "catalog_applications", "catalog_models", "strategy_catalog"} {
		counts[table] = len(state.Tables[table])
	}
	return map[string]any{"version": state.Version, "createdAt": state.CreatedAt, "counts": counts}
}
func (s *Server) inspectState(response http.ResponseWriter, request *http.Request) {
	if !s.beginStateTransfer(response) {
		return
	}
	defer s.transferMu.Unlock()
	state, ok := s.readStateArchive(response, request)
	if !ok {
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, stateSummary(state))
}
func (s *Server) importState(response http.ResponseWriter, request *http.Request) {
	if !s.beginStateTransfer(response) {
		return
	}
	defer s.transferMu.Unlock()
	state, ok := s.readStateArchive(response, request)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 5*time.Minute)
	defer cancel()
	// Re-encrypt secrets with the destination vault; the source master key and
	// installation environment are never part of the archive.
	if len(state.Credentials) != len(state.Tables["credentials"]) || len(state.Objects) != len(state.Tables["artifacts"]) {
		transferError(response, transfer.ErrArchive)
		return
	}
	for _, row := range state.Tables["credentials"] {
		plain, exists := state.Credentials[transfer.String(row, "id")]
		if !exists || !json.Valid(plain) {
			transferError(response, transfer.ErrArchive)
			return
		}
		nonce, ciphertext, fingerprint, err := s.vault.Encrypt(plain)
		if err != nil {
			transferError(response, err)
			return
		}
		setBytea(row, "nonce", nonce)
		setBytea(row, "ciphertext", ciphertext)
		transfer.Set(row, "fingerprint", fingerprint)
	}
	type object struct {
		key, name, media string
		value            []byte
	}
	objects := []object{}
	dirs := map[string]bool{}
	importID := id.New("import")
	validID := func(value, prefix string) bool {
		if !strings.HasPrefix(value, prefix+"_") {
			return false
		}
		raw, e := hex.DecodeString(strings.TrimPrefix(value, prefix+"_"))
		return e == nil && len(raw) == 12
	}
	for _, row := range state.Tables["artifacts"] {
		artifactID, operationID := transfer.String(row, "id"), transfer.String(row, "operation_id")
		if !validID(artifactID, "art") || !validID(operationID, "op") {
			transferError(response, transfer.ErrArchive)
			return
		}
		plain, exists := state.Objects[artifactID]
		var size int64
		var sensitive bool
		if !exists || json.Unmarshal(row["size_bytes"], &size) != nil || json.Unmarshal(row["sensitive"], &sensitive) != nil || size > artifacts.MaxArtifactBytes || artifacts.Verify(plain, transfer.String(row, "digest"), size) != nil {
			transferError(response, transfer.ErrArchive)
			return
		}
		value := plain
		if sensitive {
			nonce, encrypted, err := s.vault.EncryptBound(plain, []byte(artifactID))
			if err != nil {
				transferError(response, err)
				return
			}
			setBytea(row, "encryption_nonce", nonce)
			value = encrypted
		} else {
			transfer.Set(row, "encryption_nonce", nil)
		}
		dir := "/kubephos/operations/" + operationID + "/imports/" + importID
		dirs[dir] = true
		key := dir + "/" + artifactID
		transfer.Set(row, "storage_key", key)
		transfer.Set(row, "storage_digest", artifacts.Digest(value))
		transfer.Set(row, "stored_size_bytes", len(value))
		objects = append(objects, object{key, artifactID, transfer.String(row, "media_type"), value})
	}
	// Validate external descriptors before committing any state. Registry changes
	// are activated only after a successful import; workers reload from storage.
	if len(state.Tables["plugin_packages"]) > 0 && s.loadPlugin == nil {
		transferError(response, errors.New("plugin loader unavailable"))
		return
	}
	for _, row := range state.Tables["plugin_packages"] {
		descriptor := []byte(transfer.String(row, "descriptor"))
		if artifacts.Digest(descriptor) != transfer.String(row, "descriptor_digest") {
			transferError(response, transfer.ErrArchive)
			return
		}
		plugin, err := s.loadPlugin(descriptor)
		if err != nil {
			transferError(response, transfer.ErrArchive)
			return
		}
		manifest := plugin.Manifest()
		if s.registry == nil || s.registry.IsBundled(manifest.ID) || manifest.ID != transfer.String(row, "plugin_id") || !manifest.Matches(transfer.String(row, "version"), transfer.String(row, "digest")) {
			transferError(response, transfer.ErrArchive)
			return
		}
	}
	keys := []string{}
	for key := range dirs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	user := request.Context().Value(authenticatedUserKey{}).(domain.User)
	err := s.store.ImportState(ctx, state, user.ID, keys, func() error {
		for _, object := range objects {
			if _, err := s.artifacts.Put(ctx, object.key, object.name, object.media, object.value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		transferError(response, err)
		return
	}
	if len(state.Tables["plugin_packages"]) > 0 {
		if err = s.reloadActivePluginPackages(ctx); err != nil {
			writeJSON(response, 200, map[string]any{"imported": true, "restartRequired": true})
			return
		}
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, 200, map[string]any{"imported": true, "restartRequired": false})
}
