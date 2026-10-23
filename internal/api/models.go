package api

import (
	"errors"
	"net/http"
	"strings"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listCatalogModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.store.ListCatalogModels(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not list models.")
		return
	}
	writeJSON(w, 200, map[string]any{"items": models})
}
func (s *Server) saveCatalogModel(w http.ResponseWriter, r *http.Request) {
	var model catalog.Model
	if err := decodeJSON(r, &model); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	create := r.Method == http.MethodPost
	if create && model.ID == "" {
		imported, err := catalog.ImportModel(r.Context(), model.URL, nil)
		if err != nil {
			writeError(w, 422, "invalid_model_url", err.Error())
			return
		}
		model = imported
	}

	if !create {
		previous, err := s.store.GetCatalogModel(r.Context(), r.PathValue("id"))
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, 404, "not_found", "Model not found.")
			return
		}
		if err != nil {
			writeError(w, 500, "database_error", "Could not read model.")
			return
		}
		if strings.TrimSpace(model.URL) != previous.URL {
			imported, err := catalog.ImportModel(r.Context(), model.URL, nil)
			if err != nil {
				writeError(w, 422, "invalid_model_url", err.Error())
				return
			}
			imported.ID = previous.ID
			imported.Enabled = model.Enabled
			model = imported
		}
	}

	model.ID = strings.TrimSpace(model.ID)
	model.URL = strings.TrimSpace(model.URL)
	model.SHA256 = strings.ToLower(strings.TrimSpace(model.SHA256))
	model.Quantization = strings.TrimSpace(model.Quantization)
	if model.Backend == "" {
		model.Backend = "llama-cpp"
	}
	if model.Quantization == "" {
		model.Quantization = "unknown"
	}
	if !create && model.ID != r.PathValue("id") {
		writeError(w, 422, "invalid_model", "The model name cannot be changed.")
		return
	}
	if err := catalog.ValidateModel(model); err != nil {
		writeError(w, 422, "invalid_model", err.Error())
		return
	}
	result, err := s.store.SaveCatalogModel(r.Context(), model, create)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, 404, "not_found", "Model not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "model_conflict", "The model already exists, or disabling it would leave no active model.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not save model.")
		return
	}
	status := http.StatusOK
	if create {
		status = http.StatusCreated
	}
	writeJSON(w, status, result)
}
func (s *Server) archiveCatalogModel(w http.ResponseWriter, r *http.Request) {
	err := s.store.ArchiveCatalogModel(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, 404, "not_found", "Enabled model not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "model_conflict", "At least one model must remain enabled.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not archive model.")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "archived"})
}
