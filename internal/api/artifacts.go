package api

import (
	"bytes"
	"compress/gzip"
	"errors"

	"log/slog"
	"mime"
	"net/http"

	"path/filepath"

	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listAvailableArtifacts(response http.ResponseWriter, request *http.Request) {
	limit := 200
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 500 {
			limit = parsed
		}
	}
	user := requestUser(request)
	ownerID := user.ID
	if user.Role == "admin" {
		ownerID = ""
	}
	items, err := s.store.ListAvailableArtifactsVisibleToUser(request.Context(), ownerID, limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list verified artifacts.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) downloadArtifact(response http.ResponseWriter, request *http.Request) {
	artifact, err := s.store.GetArtifact(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Artifact not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read artifact metadata.")
		return
	}
	if !s.authorizeOperation(response, request, artifact.OperationID) {
		return
	}
	if artifact.Sensitive {
		writeError(response, http.StatusForbidden, "sensitive_artifact", "Sensitive artifacts cannot be downloaded from the browser.")
		return
	}
	value, err := s.readArtifactPayload(request.Context(), artifact)
	if err != nil {
		writeError(response, http.StatusBadGateway, "storage_error", "Artifact integrity verification failed.")
		return
	}
	response.Header().Set("Content-Type", artifact.MediaType)
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifactFilename(artifact)}))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	writeArtifactBody(response, request, artifact, value)
}

func writeArtifactBody(response http.ResponseWriter, request *http.Request, artifact domain.Artifact, value []byte) {
	if artifact.Type == "TimeSeriesDataset" {
		response.Header().Add("Vary", "Accept-Encoding")
		if acceptsGzip(request.Header.Get("Accept-Encoding")) && len(value) > 1024 {
			var compressed bytes.Buffer
			writer, _ := gzip.NewWriterLevel(&compressed, gzip.BestSpeed)
			_, _ = writer.Write(value)
			if err := writer.Close(); err == nil {
				value = compressed.Bytes()
				response.Header().Set("Content-Encoding", "gzip")
			}
		}
	}
	if _, err := response.Write(value); err != nil {
		slog.Error("stream artifact", "artifact", artifact.ID, "error", err)
	}
}

func artifactFilename(artifact domain.Artifact) string {
	name := strings.TrimSpace(artifact.Name)
	if filepath.Ext(name) != "" {
		return name
	}
	mediaType := strings.TrimSpace(strings.SplitN(artifact.MediaType, ";", 2)[0])
	extensions, _ := mime.ExtensionsByType(mediaType)
	if len(extensions) > 0 {
		return name + extensions[0]
	}
	return name
}

func acceptsGzip(value string) bool {
	for _, entry := range strings.Split(value, ",") {
		parts := strings.Split(entry, ";")
		if !strings.EqualFold(strings.TrimSpace(parts[0]), "gzip") {
			continue
		}
		allowed := true
		for _, parameter := range parts[1:] {
			pair := strings.SplitN(strings.TrimSpace(parameter), "=", 2)
			if len(pair) == 2 && strings.EqualFold(pair[0], "q") {
				quality, err := strconv.ParseFloat(strings.TrimSpace(pair[1]), 64)
				if err != nil || quality <= 0 || quality > 1 {
					allowed = false
				}
			}
		}
		return allowed
	}
	return false
}
