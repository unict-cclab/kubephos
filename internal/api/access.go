package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

type resourceAccessIdentity struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type resourceAccessEndpoint struct {
	Name        string                   `json:"name"`
	URL         string                   `json:"url,omitempty"`
	Service     string                   `json:"service,omitempty"`
	Credentials []resourceAccessIdentity `json:"credentials"`
}

type accessDocument struct {
	Kind     string `json:"kind"`
	Metadata struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"metadata"`
	Spec struct {
		Host      string `json:"host"`
		URL       string `json:"url"`
		Server    string `json:"server"`
		Service   string `json:"service"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Endpoints []struct {
			Name        string `json:"name"`
			Service     string `json:"service"`
			ServiceName string `json:"serviceName"`
			Scheme      string `json:"scheme"`
			NodePort    int    `json:"nodePort"`
		} `json:"endpoints"`
	} `json:"spec"`
}

func (s *Server) revealManagedResourceAccess(response http.ResponseWriter, request *http.Request) {
	user, ok := request.Context().Value(authenticatedUserKey{}).(domain.User)
	if !ok || user.Role != "admin" {
		writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
		return
	}
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Managed resource not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the managed resource.")
		return
	}
	runID := resource.RecreationPipelineRunID
	if runID == "" {
		runID = resource.PipelineRunID
	}
	if runID == "" {
		writeError(response, http.StatusConflict, "access_unavailable", "This resource does not have a provisioned access profile.")
		return
	}
	run, err := s.store.GetPipelineRun(request.Context(), runID)
	if err != nil {
		writeError(response, http.StatusConflict, "access_unavailable", "The resource lifecycle is unavailable.")
		return
	}
	documents := []json.RawMessage{}
	seenOperations := map[string]bool{}
	for _, stage := range run.Stages {
		for _, operationID := range []string{stage.OperationID, stage.CleanupOperationID} {
			if operationID == "" || seenOperations[operationID] {
				continue
			}
			seenOperations[operationID] = true
			artifacts, err := s.store.ListArtifacts(request.Context(), operationID)
			if err != nil {
				writeError(response, http.StatusInternalServerError, "database_error", "Could not read the resource access artifacts.")
				return
			}
			for _, artifact := range artifacts {
				if !accessArtifactType(artifact.Type) || artifact.VerifiedAt.IsZero() {
					continue
				}
				value, err := s.readArtifactPayload(request.Context(), artifact)
				if err != nil {
					writeError(response, http.StatusBadGateway, "access_integrity_error", "A protected access artifact failed integrity verification.")
					return
				}
				documents = append(documents, value)
			}
		}
	}
	var specification map[string]any
	_ = json.Unmarshal(resource.Spec, &specification)
	address, _ := specification["addressStart"].(string)
	items := resourceAccessFromDocuments(documents, address)
	response.Header().Set("Cache-Control", "no-store, max-age=0")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func accessArtifactType(value string) bool {
	switch value {
	case "RegistryEndpoint", "RegistryCredential", "ServiceCredential", "ObservabilityCapability", "ManagedPlatformCapability":
		return true
	default:
		return false
	}
}

func resourceAccessFromDocuments(values []json.RawMessage, address string) []resourceAccessEndpoint {
	endpoints := map[string]*resourceAccessEndpoint{}
	credentials := []accessDocument{}
	for _, raw := range values {
		var document accessDocument
		if json.Unmarshal(raw, &document) != nil {
			continue
		}
		switch document.Kind {
		case "RegistryEndpoint":
			endpointURL := document.Spec.URL
			if endpointURL == "" && document.Spec.Host != "" {
				endpointURL = "https://" + document.Spec.Host
			}
			key := "registry:" + document.Spec.Host
			endpoints[key] = &resourceAccessEndpoint{Name: displayAccessName(document.Metadata.Name, "Harbor"), URL: endpointURL, Service: document.Spec.Host, Credentials: []resourceAccessIdentity{}}
		case "RegistryCredential", "ServiceCredential":
			credentials = append(credentials, document)
		default:
			for _, endpoint := range document.Spec.Endpoints {
				service := endpoint.ServiceName
				if service == "" {
					service = endpoint.Service
				}
				endpointURL := ""
				if address != "" && endpoint.NodePort > 0 {
					scheme := endpoint.Scheme
					if scheme == "" {
						scheme = "http"
					}
					endpointURL = scheme + "://" + address + ":" + strconv.Itoa(endpoint.NodePort)
					if strings.EqualFold(endpoint.Name, "kiali") {
						endpointURL += "/kiali"
					}
				}
				key := "service:" + service
				endpoints[key] = &resourceAccessEndpoint{Name: displayAccessName(endpoint.Name, service), URL: endpointURL, Service: service, Credentials: []resourceAccessIdentity{}}
			}
		}
	}
	for _, credential := range credentials {
		identity := resourceAccessIdentity{Name: displayAccessName(credential.Metadata.Name, credential.Metadata.Role), Role: credential.Metadata.Role, Username: credential.Spec.Username, Password: credential.Spec.Password}
		key := "service:" + credential.Spec.Service
		if credential.Kind == "RegistryCredential" {
			key = "registry:" + credential.Spec.Server
		}
		endpoint := endpoints[key]
		if endpoint == nil {
			endpointURL := ""
			service := credential.Spec.Service
			if credential.Spec.Server != "" {
				endpointURL = registryURL(credential.Spec.Server)
				service = credential.Spec.Server
			}
			endpoint = &resourceAccessEndpoint{Name: displayAccessName(service, credential.Kind), URL: endpointURL, Service: service, Credentials: []resourceAccessIdentity{}}
			endpoints[key] = endpoint
		}
		endpoint.Credentials = append(endpoint.Credentials, identity)
	}
	result := make([]resourceAccessEndpoint, 0, len(endpoints))
	for _, endpoint := range endpoints {
		sort.Slice(endpoint.Credentials, func(left, right int) bool {
			return endpoint.Credentials[left].Role < endpoint.Credentials[right].Role
		})
		result = append(result, *endpoint)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result
}

func displayAccessName(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	parts := strings.FieldsFunc(value, func(character rune) bool { return character == '-' || character == '_' })
	for index := range parts {
		if len(parts[index]) > 0 {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, " ")
}

func registryURL(value string) string {
	if parsed, err := url.Parse(value); err == nil && parsed.Scheme != "" {
		return value
	}
	return "https://" + value
}
