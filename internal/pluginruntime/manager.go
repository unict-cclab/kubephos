package pluginruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/storage"
)

var ErrDisabled = errors.New("external OCI plugins are not enabled")

type ProfileStore interface {
	GetPluginRuntimeProfile(context.Context) (domain.PluginRuntimeProfile, error)
	SavePluginRuntimeProfile(context.Context, domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error)
	DeletePluginRuntimeProfile(context.Context) error
	GetArtifact(context.Context, string) (domain.Artifact, error)
	GetOperation(context.Context, string) (domain.Operation, error)
}

type ArtifactStore interface {
	ReadVerified(context.Context, string, string, int64) ([]byte, error)
}

type Vault interface {
	DecryptBound([]byte, []byte, []byte) ([]byte, error)
}

type Manager struct {
	store     ProfileStore
	artifacts ArtifactStore
	vault     Vault
	fallback  plugins.ContainerRunner
	client    *http.Client
}

type executorEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Host      string `json:"host"`
		Address   string `json:"address"`
		Port      int    `json:"port"`
		Transport string `json:"transport"`
		Security  string `json:"security"`
	} `json:"spec"`
}

type executorCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Role string `json:"role"`
	} `json:"metadata"`
	Spec struct {
		CA          string `json:"ca"`
		Certificate string `json:"certificate"`
		PrivateKey  string `json:"privateKey"`
	} `json:"spec"`
}

type registryEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Protocol string   `json:"protocol"`
		Host     string   `json:"host"`
		URL      string   `json:"url"`
		CABundle string   `json:"caBundle"`
		Insecure bool     `json:"insecure"`
		Projects []string `json:"projects"`
	} `json:"spec"`
}

type registryCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Role string `json:"role"`
	} `json:"metadata"`
	Spec struct {
		Server   string   `json:"server"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		Project  string   `json:"project"`
		Scopes   []string `json:"scopes"`
	} `json:"spec"`
}

type resolvedProfile struct {
	host       string
	ca         string
	cert       string
	key        string
	registries []plugins.RegistryAccess
}

func NewManager(store ProfileStore, artifactStore ArtifactStore, vault Vault, fallback plugins.ContainerRunner) *Manager {
	return &Manager{store: store, artifacts: artifactStore, vault: vault, fallback: fallback, client: &http.Client{Timeout: 5 * time.Second}}
}

func (m *Manager) Profile(ctx context.Context) (domain.PluginRuntimeProfile, error) {
	if m.store == nil {
		return domain.PluginRuntimeProfile{}, errors.New("plugin runtime profile store is unavailable")
	}
	return m.store.GetPluginRuntimeProfile(ctx)
}

func (m *Manager) ValidateProfile(ctx context.Context, profile domain.PluginRuntimeProfile) error {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return err
	}
	resolved, err := m.resolveProfile(ctx, profile)
	if err != nil {
		return err
	}
	runner, cleanup, err := resolved.runner()
	if err != nil {
		return err
	}
	defer cleanup()
	if err := runner.Ready(ctx); err != nil {
		return fmt.Errorf("executor health gate failed: %w", err)
	}
	for _, registry := range resolved.registries {
		if err := m.probeRegistry(ctx, registry); err != nil {
			return fmt.Errorf("registry %s health gate failed: %w", registry.Authority, err)
		}
	}
	return nil
}

func (m *Manager) ActivateProfile(ctx context.Context, profile domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error) {
	profile, err := normalizeProfile(profile)
	if err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	if err := m.ValidateProfile(ctx, profile); err != nil {
		return domain.PluginRuntimeProfile{}, err
	}
	return m.store.SavePluginRuntimeProfile(ctx, profile)
}

func (m *Manager) DeactivateProfile(ctx context.Context) error {
	return m.store.DeletePluginRuntimeProfile(ctx)
}

func (m *Manager) Ready(ctx context.Context) error {
	runner, cleanup, err := m.acquire(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return runner.Ready(ctx)
}

func (m *Manager) Run(ctx context.Context, image, command string, payload []byte, network bool, log plugins.Logger) ([]byte, []string, error) {
	runner, cleanup, err := m.acquire(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	return runner.Run(ctx, image, command, payload, network, log)
}

func (m *Manager) ValidateImage(image string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	runner, cleanup, err := m.acquire(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	return plugins.ValidateRuntimeImage(runner, image)
}

func (m *Manager) EvictImage(ctx context.Context, image string) error {
	runner, cleanup, err := m.acquire(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	cache, ok := runner.(plugins.ContainerImageCache)
	if !ok {
		return nil
	}
	return cache.EvictImage(ctx, image)
}

func (m *Manager) Environment(ctx context.Context) ([]string, func(), error) {
	runner, cleanup, err := m.acquire(ctx)
	if err != nil {
		return nil, func() {}, err
	}
	provider, ok := runner.(plugins.RuntimeEnvironment)
	if !ok {
		cleanup()
		return nil, func() {}, errors.New("OCI executor does not expose a build environment")
	}
	values, release, err := provider.Environment(ctx)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return values, func() { release(); cleanup() }, nil
}

func (m *Manager) State(ctx context.Context) (string, string) {
	if m.store == nil {
		return "unavailable", "Plugin runtime profile store is unavailable."
	}
	if _, err := m.store.GetPluginRuntimeProfile(ctx); errors.Is(err, storage.ErrNotFound) && m.fallback == nil {
		return "disabled", "External OCI plugins are not enabled."
	} else if err != nil && !errors.Is(err, storage.ErrNotFound) {
		return "unavailable", err.Error()
	}
	if err := m.Ready(ctx); err != nil {
		if errors.Is(err, ErrDisabled) {
			return "disabled", "External OCI plugins are not enabled."
		}
		return "unavailable", err.Error()
	}
	return "healthy", "The dedicated OCI executor is ready."
}

func (m *Manager) acquire(ctx context.Context) (plugins.ContainerRunner, func(), error) {
	if m.store == nil {
		return nil, func() {}, errors.New("plugin runtime profile store is unavailable")
	}
	profile, err := m.store.GetPluginRuntimeProfile(ctx)
	if errors.Is(err, storage.ErrNotFound) {
		if m.fallback == nil {
			return nil, func() {}, ErrDisabled
		}
		return m.fallback, func() {}, nil
	}
	if err != nil {
		return nil, func() {}, err
	}
	resolved, err := m.resolveProfile(ctx, profile)
	if err != nil {
		return nil, func() {}, err
	}
	return resolved.runner()
}

func (m *Manager) resolveProfile(ctx context.Context, profile domain.PluginRuntimeProfile) (resolvedProfile, error) {
	endpointValue, endpointArtifact, err := m.readArtifact(ctx, profile.EndpointArtifactID, "OCIExecutorEndpoint", false)
	if err != nil {
		return resolvedProfile{}, err
	}
	credentialValue, credentialArtifact, err := m.readArtifact(ctx, profile.CredentialArtifactID, "OCIExecutorCredential", true)
	if err != nil {
		return resolvedProfile{}, err
	}
	if endpointArtifact.OperationID != credentialArtifact.OperationID {
		return resolvedProfile{}, errors.New("executor endpoint and credential must come from the same successful operation")
	}
	var endpoint executorEndpoint
	var credential executorCredential
	if err := json.Unmarshal(endpointValue, &endpoint); err != nil {
		return resolvedProfile{}, errors.New("executor endpoint artifact is invalid")
	}
	if err := json.Unmarshal(credentialValue, &credential); err != nil {
		return resolvedProfile{}, errors.New("executor credential artifact is invalid")
	}
	if err := validateExecutor(endpoint, credential); err != nil {
		return resolvedProfile{}, err
	}
	result := resolvedProfile{host: endpoint.Spec.Host, ca: credential.Spec.CA, cert: credential.Spec.Certificate, key: credential.Spec.PrivateKey}
	seenAuthorities := map[string]bool{}
	for _, source := range profile.Registries {
		endpointValue, endpointArtifact, err := m.readArtifact(ctx, source.EndpointArtifactID, "RegistryEndpoint", false)
		if err != nil {
			return resolvedProfile{}, err
		}
		credentialValue, credentialArtifact, err := m.readArtifact(ctx, source.CredentialArtifactID, "RegistryCredential", true)
		if err != nil {
			return resolvedProfile{}, err
		}
		if endpointArtifact.OperationID != credentialArtifact.OperationID {
			return resolvedProfile{}, errors.New("registry endpoint and credential must come from the same successful operation")
		}
		var endpoint registryEndpoint
		var credential registryCredential
		if err := json.Unmarshal(endpointValue, &endpoint); err != nil {
			return resolvedProfile{}, errors.New("registry endpoint artifact is invalid")
		}
		if err := json.Unmarshal(credentialValue, &credential); err != nil {
			return resolvedProfile{}, errors.New("registry credential artifact is invalid")
		}
		access, err := validateRegistry(endpoint, credential)
		if err != nil {
			return resolvedProfile{}, err
		}
		if seenAuthorities[access.Authority] {
			return resolvedProfile{}, fmt.Errorf("registry endpoint %s is selected more than once", access.Authority)
		}
		seenAuthorities[access.Authority] = true
		result.registries = append(result.registries, access)
	}
	return result, nil
}

func (m *Manager) readArtifact(ctx context.Context, artifactID, expectedType string, sensitive bool) ([]byte, domain.Artifact, error) {
	if m.artifacts == nil {
		return nil, domain.Artifact{}, errors.New("artifact storage is unavailable")
	}
	artifact, err := m.store.GetArtifact(ctx, artifactID)
	if err != nil {
		return nil, domain.Artifact{}, fmt.Errorf("read %s artifact: %w", expectedType, err)
	}
	if artifact.Type != expectedType || artifact.Version != "v1alpha1" || artifact.Sensitive != sensitive || artifact.VerifiedAt.IsZero() {
		return nil, domain.Artifact{}, fmt.Errorf("artifact %s is not a verified %s/v1alpha1 artifact", artifactID, expectedType)
	}
	operation, err := m.store.GetOperation(ctx, artifact.OperationID)
	if err != nil || operation.Status != domain.OperationSucceeded {
		return nil, domain.Artifact{}, fmt.Errorf("artifact %s does not belong to a successful operation", artifactID)
	}
	stored, err := m.artifacts.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
	if err != nil {
		return nil, domain.Artifact{}, fmt.Errorf("verify stored artifact %s: %w", artifactID, err)
	}
	value := stored
	if sensitive {
		if m.vault == nil {
			return nil, domain.Artifact{}, errors.New("artifact vault is unavailable")
		}
		value, err = m.vault.DecryptBound(artifact.EncryptionNonce, stored, []byte(artifact.ID))
		if err != nil {
			return nil, domain.Artifact{}, fmt.Errorf("decrypt artifact %s: %w", artifactID, err)
		}
	}
	if err := artifacts.Verify(value, artifact.Digest, artifact.SizeBytes); err != nil {
		return nil, domain.Artifact{}, fmt.Errorf("verify artifact %s: %w", artifactID, err)
	}
	if !json.Valid(value) {
		return nil, domain.Artifact{}, fmt.Errorf("artifact %s does not contain valid JSON", artifactID)
	}
	return value, artifact, nil
}

func (m *Manager) probeRegistry(ctx context.Context, access plugins.RegistryAccess) error {
	pool, err := certificatePool(access.CABundle)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+access.Authority+"/v2/", nil)
	if err != nil {
		return err
	}
	request.SetBasicAuth(access.Username, access.Password)
	baseClient := m.client
	if baseClient == nil {
		baseClient = &http.Client{Timeout: 5 * time.Second}
	}
	client := *baseClient
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("registry returned %s", response.Status)
	}
	return nil
}

func (r resolvedProfile) runner() (plugins.ContainerRunner, func(), error) {
	directory, err := os.MkdirTemp("", "kubephos-runtime-")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	paths := []struct {
		name  string
		value string
	}{{"ca.pem", r.ca}, {"client-cert.pem", r.cert}, {"client-key.pem", r.key}}
	for _, current := range paths {
		if err := os.WriteFile(filepath.Join(directory, current.name), []byte(current.value), 0600); err != nil {
			cleanup()
			return nil, func() {}, err
		}
	}
	registries := make([]string, 0, len(r.registries))
	for _, registry := range r.registries {
		registries = append(registries, registry.Authority)
	}
	runner := plugins.NewDockerRunnerWithAccess(r.host, filepath.Join(directory, "ca.pem"), filepath.Join(directory, "client-cert.pem"), filepath.Join(directory, "client-key.pem"), registries, r.registries)
	if runner == nil {
		cleanup()
		return nil, func() {}, errors.New("executor endpoint is empty")
	}
	return runner, cleanup, nil
}

func normalizeProfile(profile domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error) {
	profile.EndpointArtifactID = strings.TrimSpace(profile.EndpointArtifactID)
	profile.CredentialArtifactID = strings.TrimSpace(profile.CredentialArtifactID)
	if !strings.HasPrefix(profile.EndpointArtifactID, "art_") || !strings.HasPrefix(profile.CredentialArtifactID, "art_") || profile.EndpointArtifactID == profile.CredentialArtifactID {
		return domain.PluginRuntimeProfile{}, errors.New("select a verified executor endpoint and its encrypted credential")
	}
	if len(profile.Registries) == 0 || len(profile.Registries) > 16 {
		return domain.PluginRuntimeProfile{}, errors.New("select between one and sixteen managed registries")
	}
	seen := map[string]bool{profile.EndpointArtifactID: true, profile.CredentialArtifactID: true}
	for index := range profile.Registries {
		registry := &profile.Registries[index]
		registry.EndpointArtifactID = strings.TrimSpace(registry.EndpointArtifactID)
		registry.CredentialArtifactID = strings.TrimSpace(registry.CredentialArtifactID)
		if !strings.HasPrefix(registry.EndpointArtifactID, "art_") || !strings.HasPrefix(registry.CredentialArtifactID, "art_") || seen[registry.EndpointArtifactID] || seen[registry.CredentialArtifactID] {
			return domain.PluginRuntimeProfile{}, errors.New("registry endpoint and credential selections must be valid and unique")
		}
		seen[registry.EndpointArtifactID] = true
		seen[registry.CredentialArtifactID] = true
	}
	return profile, nil
}

func validateExecutor(endpoint executorEndpoint, credential executorCredential) error {
	if endpoint.APIVersion != "artifacts.kubephos.dev/v1alpha1" || endpoint.Kind != "OCIExecutorEndpoint" || endpoint.Spec.Transport != "docker" || endpoint.Spec.Security != "rootless-mtls" {
		return errors.New("executor endpoint does not implement OCIExecutorEndpoint/v1alpha1")
	}
	parsed, err := url.Parse(endpoint.Spec.Host)
	if err != nil || parsed.Scheme != "tcp" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("executor endpoint must be a dedicated TCP address")
	}
	host, port, err := net.SplitHostPort(parsed.Host)
	if err != nil || host != endpoint.Spec.Address || port != fmt.Sprint(endpoint.Spec.Port) || endpoint.Spec.Port < 1 || endpoint.Spec.Port > 65535 {
		return errors.New("executor endpoint address is inconsistent")
	}
	if credential.APIVersion != "artifacts.kubephos.dev/v1alpha1" || credential.Kind != "OCIExecutorCredential" || credential.Metadata.Role != "client" {
		return errors.New("executor credential does not implement OCIExecutorCredential/v1alpha1")
	}
	pair, err := tls.X509KeyPair([]byte(credential.Spec.Certificate), []byte(credential.Spec.PrivateKey))
	if err != nil || len(pair.Certificate) == 0 {
		return errors.New("executor client certificate and private key are invalid")
	}
	pool, err := certificatePool(credential.Spec.CA)
	if err != nil {
		return errors.New("executor certificate authority is invalid")
	}
	certificate, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("executor client certificate is invalid")
	}
	if _, err := certificate.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: time.Now()}); err != nil {
		return errors.New("executor client certificate is not trusted or active")
	}
	return nil
}

func validateRegistry(endpoint registryEndpoint, credential registryCredential) (plugins.RegistryAccess, error) {
	if endpoint.APIVersion != "artifacts.kubephos.dev/v1alpha1" || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Insecure || endpoint.Spec.Host == "" {
		return plugins.RegistryAccess{}, errors.New("registry endpoint does not implement secure RegistryEndpoint/v1alpha1")
	}
	parsed, err := url.Parse(endpoint.Spec.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != endpoint.Spec.Host || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return plugins.RegistryAccess{}, errors.New("registry endpoint URL is inconsistent")
	}
	if _, err := certificatePool(endpoint.Spec.CABundle); err != nil {
		return plugins.RegistryAccess{}, errors.New("registry certificate authority is not active")
	}
	if credential.APIVersion != "artifacts.kubephos.dev/v1alpha1" || credential.Kind != "RegistryCredential" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || !contains(credential.Spec.Scopes, "pull") {
		return plugins.RegistryAccess{}, errors.New("registry credential does not grant pull access to the selected endpoint")
	}
	if credential.Spec.Project != "" && !contains(endpoint.Spec.Projects, credential.Spec.Project) {
		return plugins.RegistryAccess{}, errors.New("registry credential project is not exposed by the selected endpoint")
	}
	return plugins.RegistryAccess{Authority: strings.ToLower(endpoint.Spec.Host), CABundle: endpoint.Spec.CABundle, Username: credential.Spec.Username, Password: credential.Spec.Password}, nil
}

func certificatePool(value string) (*x509.CertPool, error) {
	pool := x509.NewCertPool()
	rest := []byte(value)
	found := false
	now := time.Now()
	for len(strings.TrimSpace(string(rest))) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, errors.New("certificate authority is invalid")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil || !certificate.IsCA || now.Before(certificate.NotBefore) || !now.Before(certificate.NotAfter) {
			return nil, errors.New("certificate authority is invalid or inactive")
		}
		pool.AddCert(certificate)
		found = true
		rest = next
	}
	if !found {
		return nil, errors.New("certificate authority is invalid")
	}
	return pool, nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
