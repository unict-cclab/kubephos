package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"kubephos.dev/kubephos/internal/storage"
)

func attributionBase(path string) string {
	for _, resource := range []string{"catalog/applications", "catalog/strategies", "catalog/models", "experiment-configurations", "experiments", "connections", "credentials", "machine-templates", "infrastructure-services", "kubernetes-clusters", "users", "workspaces", "pipelines", "pipeline-runs", "operations", "plugin-packages", "plugin-imports"} {
		base := "/api/v1/" + resource
		if path == base || strings.HasPrefix(path, base+"/") && !strings.Contains(strings.TrimPrefix(path, base+"/"), "/") {
			return base
		}
	}
	return ""
}

func attributeResources(store *storage.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := attributionBase(r.URL.Path)
		if r.Method != http.MethodGet || base == "" {
			next.ServeHTTP(w, r)
			return
		}
		response := newBufferedResponse()
		next.ServeHTTP(response, r)
		if response.status >= 200 && response.status < 300 && strings.HasPrefix(response.header.Get("Content-Type"), "application/json") {
			var body any
			decoder := json.NewDecoder(bytes.NewReader(response.body.Bytes()))
			decoder.UseNumber()
			if decoder.Decode(&body) == nil {
				items := attributionItems(body)
				ids := []string{}
				for _, item := range items {
					if id, ok := item["id"].(string); ok {
						ids = append(ids, id)
					}
				}
				if len(ids) > 0 {
					values, err := store.ResourceAttributions(r.Context(), base, ids)
					if err != nil {
						slog.Error("read resource attribution", "resource", base, "error", err)
					} else {
						for _, item := range items {
							id, _ := item["id"].(string)
							value := values[id]
							if value.CreatedBy != "" {
								item["createdBy"] = value.CreatedBy
							}
							if value.ModifiedBy != "" {
								item["modifiedBy"] = value.ModifiedBy
							}
						}
						if data, err := json.Marshal(body); err == nil {
							response.body.Reset()
							response.body.Write(data)
						}
					}
				}
			}
		}
		for key, values := range response.header {
			w.Header()[key] = values
		}
		w.WriteHeader(response.status)
		_, _ = w.Write(response.body.Bytes())
	})
}

func attributionItems(body any) []map[string]any {
	if object, ok := body.(map[string]any); ok {
		if items, ok := object["items"]; ok {
			return attributionItems(items)
		}
		if _, ok := object["id"].(string); ok {
			return []map[string]any{object}
		}
	}
	result := []map[string]any{}
	if items, ok := body.([]any); ok {
		for _, item := range items {
			if object, ok := item.(map[string]any); ok {
				result = append(result, object)
			}
		}
	}
	return result
}
