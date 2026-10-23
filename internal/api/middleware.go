package api

import (
	"bytes"

	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"net/http"

	"strings"

	"time"

	"kubephos.dev/kubephos/internal/domain"

	"kubephos.dev/kubephos/internal/storage"
)

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (r *bufferedResponse) Header() http.Header {
	return r.header
}

func (r *bufferedResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *bufferedResponse) Write(value []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(value)
}

func auditMutations(store *storage.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		action, targetType, targetID, audited := auditTarget(request)
		if !audited {
			next.ServeHTTP(response, request)
			return
		}
		buffered := newBufferedResponse()
		next.ServeHTTP(buffered, request)
		if buffered.status == 0 {
			buffered.status = http.StatusOK
		}
		if targetID == "" && buffered.status < 300 {
			var created struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(buffered.body.Bytes(), &created) == nil {
				targetID = created.ID
			}
		}
		outcome := "succeeded"
		if buffered.status >= 500 {
			outcome = "failed"
		} else if buffered.status >= 400 {
			outcome = "rejected"
		}
		details, _ := json.Marshal(map[string]any{"method": request.Method, "path": request.URL.Path, "status": buffered.status})
		if err := store.AppendAuditEvent(request.Context(), domain.AuditEvent{Actor: actorName(request), Action: action, TargetType: targetType, TargetID: targetID, Outcome: outcome, Details: details}); err != nil {
			slog.Error("append audit event", "action", action, "error", err)
		}
		for key, values := range buffered.header {
			response.Header()[key] = values
		}
		response.WriteHeader(buffered.status)
		_, _ = response.Write(buffered.body.Bytes())
	})
}

func auditTarget(request *http.Request) (string, string, string, bool) {
	if request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodPatch && request.Method != http.MethodDelete {
		return "", "", "", false
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		switch parts[0] {
		case "workspaces":
			return "workspace.create", "workspace", "", true
		case "credentials":
			return "credential.create", "credential", "", true
		case "connections":
			return "connection.create", "connection", "", true
		case "operations":
			return "operation.create", "operation", "", true
		case "pipelines":
			return "pipeline.create", "pipeline", "", true
		case "experiments":
			return "experiment.create", "experiment", "", true
		case "experiment-runs":
			return "experiment.run.create", "experiment", "", true
		case "plugins":
			return "plugin.import", "plugin", "", true
		case "terminals":
			return "terminal.ticket.create", "ssh-terminal", "", true
		}
	}

	if len(parts) == 3 && (parts[0] == "experiments" || parts[0] == "experiment-configurations") && parts[2] == "sharing" {
		return "resource.sharing.update", parts[0], parts[1], true
	}
	// Keep a resource ID on updates as well as creation, so attribution can be read back.
	if len(parts) >= 2 {
		switch parts[0] {
		case "experiment-configurations", "users", "connections", "credentials", "machine-templates", "infrastructure-services", "kubernetes-clusters", "workspaces":
			if parts[0] != "experiment-configurations" || len(parts) < 3 || parts[2] != "clone" {
				return strings.TrimSuffix(parts[0], "s") + "." + strings.ToLower(request.Method), parts[0], parts[1], true
			}
		}
	}
	if len(parts) == 3 && parts[0] == "catalog" && parts[1] == "strategies" {
		return "strategy." + strings.ToLower(request.Method), "catalog-strategy", parts[2], true
	}
	if len(parts) == 3 && parts[0] == "pipelines" && parts[2] == "runs" {
		return "pipeline.run.create", "pipeline", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "experiments" && request.Method == http.MethodDelete {
		return "experiment.delete", "experiment", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "experiments" && parts[2] == "figures" && request.Method == http.MethodPost {
		return "experiment.figure.create", "experiment", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "experiments" && parts[2] == "cancel" {
		return "experiment.cancel", "experiment", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "plugins" && parts[1] == "inspect" {
		return "plugin.inspect", "plugin", "", true
	}
	if len(parts) == 3 && parts[0] == "plugin-packages" && parts[2] == "activate" {
		return "plugin.activate", "plugin-package", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "plugin-packages" && parts[2] == "deactivate" {
		return "plugin.deactivate", "plugin-package", parts[1], true
	}
	if len(parts) == 2 && parts[0] == "plugin-runtime" {
		switch parts[1] {
		case "validate":
			return "plugin-runtime.validate", "plugin-runtime", "default", true
		case "activate":
			return "plugin-runtime.activate", "plugin-runtime", "default", true
		case "deactivate":
			return "plugin-runtime.deactivate", "plugin-runtime", "default", true
		}
	}
	if len(parts) == 3 && parts[0] == "pipeline-runs" && parts[2] == "cancel" {
		return "pipeline.run.cancel", "pipeline-run", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "managed-resources" && parts[2] == "access" {
		return "managed-resource.access.reveal", "managed-resource", parts[1], true
	}
	if len(parts) == 3 && parts[0] == "connections" && parts[2] == "secret" {
		return "connection.secret.reveal", "connection", parts[1], true
	}
	if len(parts) >= 2 && parts[0] == "catalog" && parts[1] == "models" {
		id := ""
		if len(parts) > 2 {
			id = parts[2]
		}
		action := "model.update"
		if request.Method == http.MethodPost {
			action = "model.create"
		}
		if request.Method == http.MethodDelete {
			action = "model.archive"
		}
		return action, "catalog-model", id, true
	}

	if len(parts) == 2 && parts[0] == "catalog" && parts[1] == "applications" {
		return "application.import", "catalog-application", "", true
	}
	if len(parts) == 3 && parts[0] == "catalog" && parts[1] == "applications" {
		return "application.delete", "catalog-application", parts[2], true
	}
	if len(parts) == 3 && parts[0] == "operations" {
		switch parts[2] {
		case "queue":
			return "operation.queue", "operation", parts[1], true
		case "cancel":
			return "operation.cancel", "operation", parts[1], true
		case "cleanup":
			return "operation.cleanup.create", "operation", parts[1], true
		}
	}
	return request.Method + " " + path, "api", "", true
}

func decodeJSON(request *http.Request, target any) error {
	reader := io.LimitReader(request.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func writeSSE(response io.Writer, event string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, payload)
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		writer := &statusResponseWriter{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(writer, request)
		if strings.HasPrefix(request.URL.Path, "/api/") {
			slog.Info("request", "method", request.Method, "path", request.URL.Path, "status", writer.status, "duration", time.Since(started))
		}
	})
}

type statusResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusResponseWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusResponseWriter) Write(value []byte) (int, error) {
	return writer.ResponseWriter.Write(value)
}

func (writer *statusResponseWriter) Flush() {
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/auth/") {
			next.ServeHTTP(response, request)
			return
		}
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data: blob:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("request panic", "error", recovered)
				writeError(response, http.StatusInternalServerError, "internal_error", "Unexpected server error.")
			}
		}()
		next.ServeHTTP(response, request)
	})
}
