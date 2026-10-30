package api

import (
	"encoding/json"
	"errors"

	"net/http"

	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/schema"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listCredentials(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCredentials(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list credentials.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCredential(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name  string          `json:"name"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if !credentialKindPattern.MatchString(input.Kind) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Credential kind is invalid.")
		return
	}
	definition, found := s.credentialSchema(input.Kind)
	if !found {
		writeError(response, http.StatusUnprocessableEntity, "unsupported_kind", "No installed plugin declares this credential kind.")
		return
	}
	issues, err := schema.Validate(definition, input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", "Could not validate the credential. Check the installed plugin and try again.")
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": schema.DescribeIssues(definition, issues)})
		return
	}
	nonce, ciphertext, fingerprint, err := s.vault.Encrypt(input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "encryption_failed", "Could not encrypt credential.")
		return
	}
	credential, err := s.store.CreateCredential(request.Context(), domain.EncryptedCredential{
		Credential: domain.Credential{ID: id.New("cred"), Name: input.Name, Kind: input.Kind, Fingerprint: fingerprint},
		Nonce:      nonce, Ciphertext: ciphertext,
	})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "credential_conflict", "A credential with this name and kind already exists.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store credential.")
		return
	}
	writeJSON(response, http.StatusCreated, credential)
}

func (s *Server) credentialSchema(kind string) (json.RawMessage, bool) {
	for _, manifest := range s.registry.Manifests() {
		for _, definition := range manifest.CredentialSchemas {
			if definition.Kind == kind {
				return definition.Schema, true
			}
		}
	}
	return nil, false
}

func (s *Server) listConnections(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProviderConnections(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list provider connections.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createConnection(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name          string          `json:"name"`
		Description   string          `json:"description"`
		PluginID      string          `json:"pluginId"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if len(input.Description) > 280 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_description", "Description must not exceed 280 characters.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	manifest := plugin.Manifest()
	if manifest.Provider == "" || !manifest.HasCapability("infrastructure.discovery") {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", "Select an infrastructure discovery plugin.")
		return
	}
	issues, err := schema.Validate(manifest.Schema, input.Configuration)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", "Could not validate the connection. Check the installed plugin and try again.")
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": schema.DescribeIssues(manifest.Schema, issues)})
		return
	}
	report := plugin.Validate(request.Context(), input.Configuration)
	if !report.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": report})
		return
	}
	connection, err := s.store.CreateProviderConnection(request.Context(), domain.ProviderConnection{ID: id.New("conn"), Name: input.Name, Description: input.Description, Provider: manifest.Provider, PluginID: manifest.ID, Configuration: input.Configuration})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "connection_conflict", "A connection with this name already exists for the provider.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store provider connection.")
		return
	}
	writeJSON(response, http.StatusCreated, connection)
}

func (s *Server) deleteConnection(response http.ResponseWriter, request *http.Request) {
	err := s.store.DeleteProviderConnection(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Connection not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "connection_in_use", "Delete the resources associated with this connection first.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete provider connection.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
