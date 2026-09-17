package api

import (
	"bytes"
	"errors"
	"image/png"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

const maxFigureBytes = 8 * 1024 * 1024

var figureMetricPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,99}$`)

func figureMediaType(format string) string {
	switch format {
	case "png":
		return "image/png"
	case "pdf":
		return "application/pdf"
	default:
		return ""
	}
}

func validateFigure(value []byte, format string) error {
	if len(value) == 0 || len(value) > maxFigureBytes {
		return errors.New("figure size is outside the supported range")
	}
	if format == "pdf" {
		if !bytes.HasPrefix(value, []byte("%PDF-")) || !bytes.Contains(value[max(0, len(value)-1024):], []byte("%%EOF")) {
			return errors.New("PDF file is incomplete")
		}
		return nil
	}
	if format == "png" {
		config, err := png.DecodeConfig(bytes.NewReader(value))
		if err != nil || config.Width < 1 || config.Height < 1 || config.Width > 6000 || config.Height > 6000 || int64(config.Width)*int64(config.Height) > 25_000_000 {
			return errors.New("PNG dimensions or image header are invalid")
		}
		return nil
	}
	return errors.New("unsupported figure format")
}

func (s *Server) saveExperimentFigure(response http.ResponseWriter, request *http.Request) {
	experimentID := request.PathValue("id")
	metric := strings.TrimSpace(request.URL.Query().Get("metric"))
	format := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("format")))
	mediaType := figureMediaType(format)
	if !figureMetricPattern.MatchString(metric) || mediaType == "" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_figure", "Choose a metric and PNG or PDF format.")
		return
	}
	declaredType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || declaredType != mediaType {
		writeError(response, http.StatusUnsupportedMediaType, "invalid_media_type", "Figure content type does not match its format.")
		return
	}
	sourcesHeader := request.Header.Get("X-KubePhos-Source-Artifacts")
	if len(sourcesHeader) > 4096 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_sources", "Too many source datasets were selected.")
		return
	}
	sources := strings.Split(sourcesHeader, ",")
	if len(sources) < 1 || len(sources) > 20 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_sources", "Select one to twenty verified experiment runs.")
		return
	}
	eligible, err := s.store.EligibleExperimentFigureSources(request.Context(), experimentID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the figure sources.")
		return
	}
	seen := map[string]bool{}
	for index := range sources {
		sources[index] = strings.TrimSpace(sources[index])
		if !eligible[sources[index]] || seen[sources[index]] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_sources", "Every figure source must be a distinct verified run of this experiment.")
			return
		}
		seen[sources[index]] = true
	}
	value, err := io.ReadAll(http.MaxBytesReader(response, request.Body, maxFigureBytes+1))
	if err != nil || validateFigure(value, format) != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_figure", "The figure file is invalid or too large.")
		return
	}
	figureID := id.New("figure")
	key := "/experiments/" + experimentID + "/figures/" + figureID + "." + format
	stored, err := s.artifacts.Put(request.Context(), key, figureID+"."+format, mediaType, value)
	if err != nil {
		writeError(response, http.StatusBadGateway, "artifact_error", "Could not save the figure to object storage.")
		return
	}
	if _, err := s.artifacts.ReadVerified(request.Context(), stored.Key, stored.Digest, stored.Size); err != nil {
		writeError(response, http.StatusBadGateway, "artifact_integrity_failed", "The stored figure failed integrity verification.")
		return
	}
	figure, err := s.store.CreateExperimentFigure(request.Context(), domain.ExperimentFigure{
		ID: figureID, ExperimentID: experimentID, Metric: metric, Format: format, SourceArtifactIDs: sources,
		StorageKey: stored.Key, Digest: stored.Digest, SizeBytes: stored.Size,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not register the saved figure.")
		return
	}
	writeJSON(response, http.StatusCreated, figure)
}

func (s *Server) listExperimentFigures(response http.ResponseWriter, request *http.Request) {
	experimentID := request.PathValue("id")
	if _, err := s.store.EligibleExperimentFigureSources(request.Context(), experimentID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment.")
		return
	}
	figures, err := s.store.ListExperimentFigures(request.Context(), experimentID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list saved figures.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": figures})
}

func (s *Server) downloadExperimentFigure(response http.ResponseWriter, request *http.Request) {
	figure, err := s.store.GetExperimentFigure(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Figure not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the figure metadata.")
		return
	}
	value, err := s.artifacts.ReadVerified(request.Context(), figure.StorageKey, figure.Digest, figure.SizeBytes)
	if err != nil {
		writeError(response, http.StatusBadGateway, "artifact_integrity_failed", "The saved figure failed integrity verification.")
		return
	}
	response.Header().Set("Content-Type", figureMediaType(figure.Format))
	response.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "kubephos-" + figure.Metric + "-" + figure.ID + "." + figure.Format}))
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(value)
}
