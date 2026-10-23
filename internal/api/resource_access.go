package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/storage"
)

type resourceAccessCredential struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type resourceAccessEndpoint struct {
	Name        string                     `json:"name"`
	URL         string                     `json:"url"`
	Credentials []resourceAccessCredential `json:"credentials"`
	Service     string                     `json:"-"`
}

func (s *Server) revealManagedResourceAccess(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Resource not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read resource access.")
		return
	}
	artifacts, err := s.store.ListPipelineRunArtifacts(request.Context(), resource.PipelineRunID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read resource access.")
		return
	}
	if _, multi := multiClusterSpec(resource.Spec); multi {
		s.multiClusterResourceAccess(response, request, resource, artifacts)
		return
	}
	documents := make([]json.RawMessage, 0, len(artifacts))
	host := ""
	kubeconfig := ""
	for _, artifact := range artifacts {
		value, readErr := s.readArtifactPayload(request.Context(), artifact)
		if readErr != nil || !json.Valid(value) {
			continue
		}
		documents = append(documents, json.RawMessage(value))
		if host == "" {
			host = accessHost(value)
		}
		if artifact.Type == "ClusterConnection" {
			kubeconfig = clusterConnectionKubeconfig(value)
		}
	}
	if resource.Kind == "kubernetes-cluster" && resource.Status == "ready" && strings.TrimSpace(kubeconfig) != "" && !hasHeadlampCredential(documents) {
		token, tokenErr := readHeadlampAccessToken(request.Context(), kubeconfig)
		if tokenErr == nil {
			documents = append(documents, headlampAccessDocuments(token)...)
		}
	}
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	writeJSON(response, http.StatusOK, map[string]any{"items": resourceAccessFromDocuments(documents, host)})
}

func clusterConnectionKubeconfig(raw []byte) string {
	var document struct {
		Kind string `json:"kind"`
		Spec struct {
			Kubeconfig string `json:"kubeconfig"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &document) != nil || document.Kind != "ClusterConnection" {
		return ""
	}
	return strings.TrimSpace(document.Spec.Kubeconfig)
}

func hasHeadlampCredential(documents []json.RawMessage) bool {
	for _, raw := range documents {
		var document struct {
			Kind string `json:"kind"`
			Spec struct {
				Service  string `json:"service"`
				Password string `json:"password"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &document) == nil && document.Kind == "ServiceCredential" && document.Spec.Service == "kubephos-headlamp" && strings.TrimSpace(document.Spec.Password) != "" {
			return true
		}
	}
	return false
}

func headlampAccessDocuments(token string) []json.RawMessage {
	return []json.RawMessage{
		json.RawMessage(`{"kind":"ManagedPlatformCapability","spec":{"endpoints":[{"name":"headlamp","service":"kubephos-headlamp","scheme":"http","nodePort":30003}]}}`),
		json.RawMessage(fmt.Sprintf(`{"kind":"ServiceCredential","metadata":{"name":"headlamp-admin","role":"cluster-admin"},"spec":{"namespace":"kubephos-observability","service":"kubephos-headlamp","username":"token","password":%q}}`, token)),
	}
}

func readHeadlampAccessToken(ctx context.Context, kubeconfig string) (string, error) {
	file, err := os.CreateTemp("", "kubephos-headlamp-kubeconfig-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.WriteString(kubeconfig); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	commandContext, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	encoded, err := exec.CommandContext(commandContext, "kubectl", "--kubeconfig", path, "get", "secret", "kubephos-headlamp-token", "--namespace", "kubephos-observability", "--output", "jsonpath={.data.token}").Output()
	if err != nil {
		return "", err
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(decoded))
	if token == "" {
		return "", errors.New("Headlamp token is empty")
	}
	return token, nil
}

func resourceAccessFromDocuments(documents []json.RawMessage, fallbackHost string) []resourceAccessEndpoint {
	endpoints := map[string]*resourceAccessEndpoint{}
	type credential struct{ name, role, service, server, username, password string }
	credentials := []credential{}
	for _, raw := range documents {
		var document map[string]any
		if json.Unmarshal(raw, &document) != nil {
			continue
		}
		kind, _ := document["kind"].(string)
		spec, _ := document["spec"].(map[string]any)
		metadata, _ := document["metadata"].(map[string]any)
		switch kind {
		case "RegistryEndpoint":
			host, _ := spec["host"].(string)
			address, _ := spec["url"].(string)
			endpoints["Managed Registry"] = &resourceAccessEndpoint{Name: "Managed Registry", URL: address, Service: host, Credentials: []resourceAccessCredential{}}
		case "RegistryCredential":
			credentials = append(credentials, credential{name: stringValue(metadata["name"]), role: stringValue(metadata["role"]), server: stringValue(spec["server"]), username: stringValue(spec["username"]), password: stringValue(spec["password"])})
		case "ServiceCredential":
			credentials = append(credentials, credential{name: stringValue(metadata["name"]), role: stringValue(metadata["role"]), service: stringValue(spec["service"]), username: stringValue(spec["username"]), password: stringValue(spec["password"])})
		case "ObservabilityCapability", "ManagedPlatformCapability":
			items, _ := spec["endpoints"].([]any)
			for _, rawEndpoint := range items {
				item, _ := rawEndpoint.(map[string]any)
				name := strings.TrimSpace(stringValue(item["name"]))
				if name == "" {
					continue
				}
				service := stringValue(item["serviceName"])
				if service == "" {
					service = stringValue(item["service"])
				}
				scheme := stringValue(item["scheme"])
				if scheme == "" {
					scheme = "http"
				}
				port := integerValue(item["nodePort"])
				address := stringValue(item["url"])
				if address == "" && fallbackHost != "" && port > 0 {
					address = fmt.Sprintf("%s://%s:%d", scheme, fallbackHost, port)
				}
				title := strings.ToUpper(name[:1]) + name[1:]
				if strings.EqualFold(name, "kiali") && !strings.HasSuffix(address, "/kiali") {
					address = strings.TrimRight(address, "/") + "/kiali"
				}
				endpoints[title] = &resourceAccessEndpoint{Name: title, URL: address, Service: service, Credentials: []resourceAccessCredential{}}
			}
		}
	}
	result := make([]resourceAccessEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint.Credentials == nil {
			endpoint.Credentials = []resourceAccessCredential{}
		}
		for _, item := range credentials {
			if item.service != "" && item.service == endpoint.Service || item.server != "" && endpoint.Service != "" && strings.Contains(endpoint.Service, item.server) {
				endpoint.Credentials = append(endpoint.Credentials, resourceAccessCredential{Name: item.name, Role: item.role, Username: item.username, Password: item.password})
			}
		}
		result = append(result, *endpoint)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func accessHost(raw []byte) string {
	var document struct {
		Spec map[string]any `json:"spec"`
	}
	if json.Unmarshal(raw, &document) != nil {
		return ""
	}
	for _, key := range []string{"server", "endpoint", "host"} {
		value := stringValue(document.Spec[key])
		if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
			return parsed.Hostname()
		}
	}
	return ""
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func integerValue(value any) int {
	result, _ := value.(float64)
	return int(result)
}
