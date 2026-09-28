package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) getConnectionDetails(response http.ResponseWriter, request *http.Request) {
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Connection not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not load connection details.")
		return
	}
	var configuration map[string]any
	if err := json.Unmarshal(connection.Configuration, &configuration); err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_configuration", "Saved connection configuration is invalid.")
		return
	}
	credential, err := s.connectionCredential(request.Context(), configuration)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "credential_error", "Could not load the connection token.")
		return
	}
	details := map[string]any{"endpoint": configuration["endpoint"], "verifyTLS": configuration["verifyTLS"], "tokenId": credential["tokenId"], "tokenSecret": "••••••••"}
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, http.StatusOK, map[string]any{"id": connection.ID, "name": connection.Name, "description": connection.Description, "provider": connection.Provider, "pluginId": connection.PluginID, "createdAt": connection.CreatedAt, "updatedAt": connection.UpdatedAt, "configuration": details})
}

func (s *Server) revealConnectionSecret(response http.ResponseWriter, request *http.Request) {
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Connection not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not load connection details.")
		return
	}
	var configuration map[string]any
	if json.Unmarshal(connection.Configuration, &configuration) != nil {
		writeError(response, http.StatusInternalServerError, "invalid_configuration", "Saved connection configuration is invalid.")
		return
	}
	credential, err := s.connectionCredential(request.Context(), configuration)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "credential_error", "Could not reveal the connection token.")
		return
	}
	secret, _ := credential["tokenSecret"].(string)
	if secret == "" {
		writeError(response, http.StatusConflict, "secret_unavailable", "The connection token secret is unavailable.")
		return
	}
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, http.StatusOK, map[string]string{"tokenSecret": secret})
}

func (s *Server) connectionCredential(ctx context.Context, configuration map[string]any) (map[string]any, error) {
	reference, _ := configuration["credentialRef"].(string)
	if reference == "" || s.vault == nil {
		return nil, errors.New("credential reference is unavailable")
	}
	credential, err := s.store.GetEncryptedCredential(ctx, reference)
	if err != nil {
		return nil, err
	}
	value, err := s.vault.Decrypt(credential.Nonce, credential.Ciphertext)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(value, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func redactConnectionInputs(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(item))
		for name, field := range item {
			lower := strings.ToLower(name)
			reference := strings.HasSuffix(lower, "ref") || strings.HasSuffix(lower, "id")
			if !reference && (strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "private") || strings.Contains(lower, "credential") || strings.Contains(lower, "kubeconfig") || strings.Contains(lower, "apikey") || strings.Contains(lower, "accesskey")) {
				result[name] = "••••••••"
			} else {
				result[name] = redactConnectionInputs(field)
			}
		}
		return result
	case []any:
		result := make([]any, len(item))
		for index, field := range item {
			result[index] = redactConnectionInputs(field)
		}
		return result
	default:
		return item
	}
}
