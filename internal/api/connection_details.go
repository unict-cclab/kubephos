package api

import (
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
	var configuration any
	if err := json.Unmarshal(connection.Configuration, &configuration); err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_configuration", "Saved connection configuration is invalid.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"id": connection.ID, "name": connection.Name, "provider": connection.Provider, "pluginId": connection.PluginID, "createdAt": connection.CreatedAt, "updatedAt": connection.UpdatedAt, "configuration": redactConnectionInputs(configuration)})
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
