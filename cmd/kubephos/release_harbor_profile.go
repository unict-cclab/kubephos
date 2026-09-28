package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedregistry"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

type harborRegistryRecord struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
	Type string `json:"type"`
}

type harborProjectRecord struct {
	Name       string `json:"name"`
	RegistryID int64  `json:"registry_id"`
}

func releaseHarborProfileSync(configValue config.Config) error {
	checking := len(os.Args) == 5 && os.Args[4] == "--check"
	if len(os.Args) != 4 && !checking || !strings.HasPrefix(os.Args[3], "svc_") {
		return errors.New("usage: kubephos release harbor-profile-sync <harbor-resource-id> [--check]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 30*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	harbor, endpoint, credential, err := loadHarborManagementTarget(ctx, configValue, store, os.Args[3])
	if err != nil {
		return err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(endpoint.Spec.CABundle)) {
		return errors.New("managed Harbor certificate authority is invalid")
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}}
	baseURL := "https://" + endpoint.Spec.Host
	temporary, err := os.MkdirTemp("", "kubephos-harbor-profile-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	if err := os.WriteFile(filepath.Join(temporary, "ca.crt"), []byte(endpoint.Spec.CABundle), 0600); err != nil {
		return err
	}
	authentication, _ := json.Marshal(map[string]any{"auths": map[string]any{endpoint.Spec.Host: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(credential.Spec.Username + ":" + credential.Spec.Password))}}})
	authenticationPath := filepath.Join(temporary, "auth.json")
	if err := os.WriteFile(authenticationPath, authentication, 0600); err != nil {
		return err
	}
	if _, err := harborAPI(ctx, client, credential, http.MethodGet, baseURL+"/api/v2.0/health", nil, http.StatusOK); err != nil {
		return fmt.Errorf("managed Harbor health check failed: %w", err)
	}
	for _, target := range managedregistry.Catalog() {
		if err := ensureHarborProxyTarget(ctx, client, credential, baseURL, temporary, authenticationPath, target, checking); err != nil {
			return err
		}
	}
	if !checking {
		details, _ := json.Marshal(map[string]int{"proxyCaches": len(managedregistry.Catalog())})
		if err := store.AppendAuditEvent(ctx, domain.AuditEvent{Actor: "maintenance", Action: "managed.harbor.profile.sync", TargetType: "harbor", TargetID: harbor.ID, Outcome: "succeeded", Details: details}); err != nil {
			return err
		}
	}
	fmt.Printf("PASS harbor=%s proxy-caches=%d mode=%s\n", harbor.Name, len(managedregistry.Catalog()), map[bool]string{true: "check", false: "sync"}[checking])
	return nil
}

func loadHarborManagementTarget(ctx context.Context, configValue config.Config, store *storage.Store, harborID string) (domain.ManagedResource, releaseRegistryEndpoint, releaseRegistryCredential, error) {
	harbor, err := store.GetManagedResource(ctx, harborID)
	if err != nil || harbor.Kind != "harbor" || harbor.Status != "ready" || harbor.PipelineRunID == "" {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("select a ready managed Harbor resource")
	}
	endpointArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-endpoint")
	if err != nil || endpointArtifact.Type != "RegistryEndpoint" || endpointArtifact.Sensitive {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("verified registry endpoint is unavailable")
	}
	credentialArtifact, err := store.GetPipelineRunArtifact(ctx, harbor.PipelineRunID, "registry-management-credential")
	if err != nil || credentialArtifact.Type != "RegistryCredential" || !credentialArtifact.Sensitive {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("verified registry management identity is unavailable")
	}
	vault, err := secrets.Open(configValue.CredentialKeyFile)
	if err != nil {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	endpointValue, err := readReleaseArtifact(ctx, artifactStore, vault, endpointArtifact)
	if err != nil {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	credentialValue, err := readReleaseArtifact(ctx, artifactStore, vault, credentialArtifact)
	if err != nil {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, err
	}
	var endpoint releaseRegistryEndpoint
	var credential releaseRegistryCredential
	if json.Unmarshal(endpointValue, &endpoint) != nil || json.Unmarshal(credentialValue, &credential) != nil || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Host == "" || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") || credential.Kind != "RegistryCredential" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" {
		return domain.ManagedResource{}, releaseRegistryEndpoint{}, releaseRegistryCredential{}, errors.New("managed Harbor endpoint and management identity are incompatible")
	}
	return harbor, endpoint, credential, nil
}

func ensureHarborProxyTarget(ctx context.Context, client *http.Client, credential releaseRegistryCredential, baseURL, certificateDirectory, authenticationPath string, target managedregistry.Target, checking bool) error {
	if len(target.StaticRepositories) > 0 {
		return ensureHarborStaticTarget(ctx, client, credential, baseURL, certificateDirectory, authenticationPath, target, checking)
	}
	registryName := target.Source + "-upstream"
	registry, found, err := findHarborRegistry(ctx, client, credential, baseURL, registryName, target.Upstream)
	if err != nil {
		return err
	}
	if !found {
		if checking {
			return fmt.Errorf("Harbor upstream registry %s is unavailable", registryName)
		}
		payload, _ := json.Marshal(map[string]string{"name": registryName, "url": target.Upstream, "type": target.Type})
		if _, err := harborAPI(ctx, client, credential, http.MethodPost, baseURL+"/api/v2.0/registries", payload, http.StatusCreated, http.StatusConflict); err != nil {
			return fmt.Errorf("create Harbor upstream registry %s: %w", registryName, err)
		}
		registry, found, err = findHarborRegistry(ctx, client, credential, baseURL, registryName, target.Upstream)
		if err != nil || !found {
			return fmt.Errorf("verify Harbor upstream registry %s: %w", registryName, firstError(err, errors.New("registry was not returned")))
		}
	}
	project, found, err := findHarborProject(ctx, client, credential, baseURL, target.Project)
	if err != nil {
		return err
	}
	if !found {
		if checking {
			return fmt.Errorf("Harbor proxy project %s is unavailable", target.Project)
		}
		payload, _ := json.Marshal(map[string]any{"project_name": target.Project, "registry_id": registry.ID, "public": true, "metadata": map[string]string{"public": "true"}})
		if _, err := harborAPI(ctx, client, credential, http.MethodPost, baseURL+"/api/v2.0/projects", payload, http.StatusCreated, http.StatusConflict); err != nil {
			return fmt.Errorf("create Harbor proxy project %s: %w", target.Project, err)
		}
		project, found, err = findHarborProject(ctx, client, credential, baseURL, target.Project)
		if err != nil || !found {
			return fmt.Errorf("verify Harbor proxy project %s: %w", target.Project, firstError(err, errors.New("project was not returned")))
		}
	}
	if project.RegistryID != registry.ID || project.RegistryID == 0 {
		return fmt.Errorf("Harbor proxy project %s points to an unexpected upstream registry", target.Project)
	}
	manifestURL := baseURL + "/v2/" + target.Project + "/" + target.Repository + "/manifests/" + target.Reference
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(credential.Spec.Username, credential.Spec.Password)
	request.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("verify Harbor proxy pull for %s: %w", target.Source, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("verify Harbor proxy pull for %s: unexpected HTTP %d", target.Source, response.StatusCode)
	}
	return nil
}

func ensureHarborStaticTarget(ctx context.Context, client *http.Client, credential releaseRegistryCredential, baseURL, certificateDirectory, authenticationPath string, target managedregistry.Target, checking bool) error {
	project, found, err := findHarborProject(ctx, client, credential, baseURL, target.Project)
	if err != nil {
		return err
	}
	if !found {
		if checking {
			return fmt.Errorf("Harbor managed mirror project %s is unavailable", target.Project)
		}
		payload, _ := json.Marshal(map[string]any{"project_name": target.Project, "public": true, "metadata": map[string]string{"public": "true"}})
		if _, err := harborAPI(ctx, client, credential, http.MethodPost, baseURL+"/api/v2.0/projects", payload, http.StatusCreated, http.StatusConflict); err != nil {
			return fmt.Errorf("create Harbor managed mirror project %s: %w", target.Project, err)
		}
		project, found, err = findHarborProject(ctx, client, credential, baseURL, target.Project)
		if err != nil || !found {
			return fmt.Errorf("verify Harbor managed mirror project %s: %w", target.Project, firstError(err, errors.New("project was not returned")))
		}
	}
	if project.RegistryID != 0 {
		return fmt.Errorf("Harbor managed mirror project %s unexpectedly references an upstream registry", target.Project)
	}
	for _, repository := range target.StaticRepositories {
		if !checking {
			source := "docker://" + target.Source + "/" + repository + ":" + target.Reference
			destination := "docker://" + strings.TrimPrefix(baseURL, "https://") + "/" + target.Project + "/" + repository + ":" + target.Reference
			sourceDigest, err := inspectImageDigest(ctx, source)
			if err != nil {
				return fmt.Errorf("inspect managed %s/%s source image: %w", target.Source, repository, err)
			}
			destinationDigest, _ := inspectImageDigest(ctx, "--authfile", authenticationPath, "--cert-dir", certificateDirectory, destination)
			if destinationDigest != sourceDigest {
				command := exec.CommandContext(ctx, "skopeo", "copy", "--retry-times", "3", "--dest-authfile", authenticationPath, "--dest-cert-dir", certificateDirectory, source, destination)
				output, err := command.CombinedOutput()
				if err != nil {
					return fmt.Errorf("seed managed %s/%s image: %w: %s", target.Source, repository, err, strings.TrimSpace(string(output)))
				}
				destinationDigest, err = inspectImageDigest(ctx, "--authfile", authenticationPath, "--cert-dir", certificateDirectory, destination)
				if err != nil || destinationDigest != sourceDigest {
					return fmt.Errorf("verify managed %s/%s image digest: %w", target.Source, repository, firstError(err, errors.New("source and Harbor digests differ")))
				}
			}
		}
		manifestURL := baseURL + "/v2/" + target.Project + "/" + repository + "/manifests/" + target.Reference
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
		if err != nil {
			return err
		}
		request.SetBasicAuth(credential.Spec.Username, credential.Spec.Password)
		request.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("verify managed %s/%s image: %w", target.Source, repository, err)
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("verify managed %s/%s image: unexpected HTTP %d", target.Source, repository, response.StatusCode)
		}
	}
	return nil
}

func inspectImageDigest(ctx context.Context, arguments ...string) (string, error) {
	commandArguments := append([]string{"inspect", "--raw"}, arguments...)
	raw, err := exec.CommandContext(ctx, "skopeo", commandArguments...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("skopeo inspect: %w: %s", err, strings.TrimSpace(string(raw)))
	}
	return imageManifestDigest(raw, runtime.GOOS, runtime.GOARCH)
}

func imageManifestDigest(raw []byte, operatingSystem, architecture string) (string, error) {
	var index struct {
		Manifests []struct {
			Digest   string `json:"digest"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
				Variant      string `json:"variant"`
			} `json:"platform"`
		} `json:"manifests"`
	}
	if json.Unmarshal(raw, &index) == nil && len(index.Manifests) > 0 {
		for _, manifest := range index.Manifests {
			if manifest.Platform.OS == operatingSystem && manifest.Platform.Architecture == architecture && manifest.Platform.Variant == "" && validImageDigest(manifest.Digest) {
				return manifest.Digest, nil
			}
		}
		return "", fmt.Errorf("image index does not contain %s/%s", operatingSystem, architecture)
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func validImageDigest(digest string) bool {
	decoded, decodeErr := hex.DecodeString(strings.TrimPrefix(digest, "sha256:"))
	return strings.HasPrefix(digest, "sha256:") && decodeErr == nil && len(decoded) == sha256.Size
}

func findHarborRegistry(ctx context.Context, client *http.Client, credential releaseRegistryCredential, baseURL, name, upstream string) (harborRegistryRecord, bool, error) {
	raw, err := harborAPI(ctx, client, credential, http.MethodGet, baseURL+"/api/v2.0/registries?name="+url.QueryEscape(name), nil, http.StatusOK)
	if err != nil {
		return harborRegistryRecord{}, false, err
	}
	var values []harborRegistryRecord
	if json.Unmarshal(raw, &values) != nil {
		return harborRegistryRecord{}, false, errors.New("Harbor registry inventory is invalid")
	}
	for _, value := range values {
		if value.Name == name && value.URL == upstream && value.ID > 0 {
			return value, true, nil
		}
	}
	return harborRegistryRecord{}, false, nil
}

func findHarborProject(ctx context.Context, client *http.Client, credential releaseRegistryCredential, baseURL, name string) (harborProjectRecord, bool, error) {
	raw, err := harborAPI(ctx, client, credential, http.MethodGet, baseURL+"/api/v2.0/projects?name="+url.QueryEscape(name), nil, http.StatusOK)
	if err != nil {
		return harborProjectRecord{}, false, err
	}
	var values []harborProjectRecord
	if json.Unmarshal(raw, &values) != nil {
		return harborProjectRecord{}, false, errors.New("Harbor project inventory is invalid")
	}
	for _, value := range values {
		if value.Name == name {
			return value, true, nil
		}
	}
	return harborProjectRecord{}, false, nil
}

func harborAPI(ctx context.Context, client *http.Client, credential releaseRegistryCredential, method, endpoint string, body []byte, expected ...int) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.SetBasicAuth(credential.Spec.Username, credential.Spec.Password)
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	for _, status := range expected {
		if response.StatusCode == status {
			return raw, nil
		}
	}
	detail := strings.TrimSpace(string(raw))
	if len(detail) > 500 {
		detail = detail[:500]
	}
	if detail == "" {
		return nil, fmt.Errorf("unexpected HTTP %d", response.StatusCode)
	}
	return nil, fmt.Errorf("unexpected HTTP %d: %s", response.StatusCode, detail)
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}
