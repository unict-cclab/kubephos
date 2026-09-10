package pluginruntime

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

type memoryProfileStore struct {
	profile    domain.PluginRuntimeProfile
	artifacts  map[string]domain.Artifact
	operations map[string]domain.Operation
}

func (s *memoryProfileStore) GetPluginRuntimeProfile(context.Context) (domain.PluginRuntimeProfile, error) {
	if s.profile.EndpointArtifactID == "" {
		return domain.PluginRuntimeProfile{}, storage.ErrNotFound
	}
	return s.profile, nil
}

func (s *memoryProfileStore) SavePluginRuntimeProfile(_ context.Context, profile domain.PluginRuntimeProfile) (domain.PluginRuntimeProfile, error) {
	s.profile = profile
	return profile, nil
}

func (s *memoryProfileStore) DeletePluginRuntimeProfile(context.Context) error {
	s.profile = domain.PluginRuntimeProfile{}
	return nil
}

func (s *memoryProfileStore) GetArtifact(_ context.Context, artifactID string) (domain.Artifact, error) {
	artifact, ok := s.artifacts[artifactID]
	if !ok {
		return domain.Artifact{}, storage.ErrNotFound
	}
	return artifact, nil
}

func (s *memoryProfileStore) GetOperation(_ context.Context, operationID string) (domain.Operation, error) {
	operation, ok := s.operations[operationID]
	if !ok {
		return domain.Operation{}, storage.ErrNotFound
	}
	return operation, nil
}

type memoryArtifactStore map[string][]byte

func (s memoryArtifactStore) ReadVerified(_ context.Context, key, _ string, _ int64) ([]byte, error) {
	value, ok := s[key]
	if !ok {
		return nil, errors.New("missing object")
	}
	return value, nil
}

type passthroughVault struct{}

func (passthroughVault) DecryptBound(_ []byte, value, _ []byte) ([]byte, error) {
	return value, nil
}

func TestNormalizeProfileRequiresUniqueVerifiedArtifactReferences(t *testing.T) {
	profile := domain.PluginRuntimeProfile{
		EndpointArtifactID: " art_executor_endpoint ", CredentialArtifactID: "art_executor_credential",
		Registries: []domain.PluginRuntimeRegistry{{EndpointArtifactID: "art_registry_endpoint", CredentialArtifactID: "art_registry_credential"}},
	}
	normalized, err := normalizeProfile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.EndpointArtifactID != "art_executor_endpoint" {
		t.Fatalf("profile was not normalized: %#v", normalized)
	}
	profile.Registries[0].CredentialArtifactID = profile.EndpointArtifactID
	if _, err := normalizeProfile(profile); err == nil {
		t.Fatal("duplicate artifact reference was accepted")
	}
}

func TestRuntimeArtifactsRequireMutualTLSAndRegistryPullAccess(t *testing.T) {
	ca, certificate, key := testIdentity(t)
	endpoint := executorEndpoint{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "OCIExecutorEndpoint"}
	endpoint.Spec.Host = "tcp://10.0.0.10:2376"
	endpoint.Spec.Address = "10.0.0.10"
	endpoint.Spec.Port = 2376
	endpoint.Spec.Transport = "docker"
	endpoint.Spec.Security = "rootless-mtls"
	credential := executorCredential{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "OCIExecutorCredential"}
	credential.Metadata.Role = "client"
	credential.Spec.CA = ca
	credential.Spec.Certificate = certificate
	credential.Spec.PrivateKey = key
	if err := validateExecutor(endpoint, credential); err != nil {
		t.Fatal(err)
	}
	endpoint.Spec.Security = "tls"
	if err := validateExecutor(endpoint, credential); err == nil {
		t.Fatal("non-rootless endpoint was accepted")
	}

	registry := registryEndpoint{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "RegistryEndpoint"}
	registry.Spec.Protocol = "oci"
	registry.Spec.Host = "registry.example.test:5443"
	registry.Spec.URL = "https://registry.example.test:5443"
	registry.Spec.CABundle = ca
	registry.Spec.Projects = []string{"development"}
	registryCredential := registryCredential{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "RegistryCredential"}
	registryCredential.Spec.Server = registry.Spec.Host
	registryCredential.Spec.Username = "robot"
	registryCredential.Spec.Password = "secret"
	registryCredential.Spec.Project = "development"
	registryCredential.Spec.Scopes = []string{"pull", "push"}
	access, err := validateRegistry(registry, registryCredential)
	if err != nil || access.Authority != registry.Spec.Host {
		t.Fatalf("valid registry was rejected: %#v %v", access, err)
	}
	registryCredential.Spec.Scopes = []string{"push"}
	if _, err := validateRegistry(registry, registryCredential); err == nil || !strings.Contains(err.Error(), "pull") {
		t.Fatalf("credential without pull access was accepted: %v", err)
	}
}

func TestResolveProfileUsesOnlyVerifiedArtifactsFromSuccessfulOperations(t *testing.T) {
	ca, certificate, key := testIdentity(t)
	executorEndpointValue := []byte(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"OCIExecutorEndpoint","spec":{"host":"tcp://10.0.0.10:2376","address":"10.0.0.10","port":2376,"transport":"docker","security":"rootless-mtls"}}`)
	executorCredentialValue, err := json.Marshal(map[string]any{
		"apiVersion": "artifacts.kubephos.dev/v1alpha1", "kind": "OCIExecutorCredential", "metadata": map[string]string{"role": "client"},
		"spec": map[string]string{"ca": ca, "certificate": certificate, "privateKey": key},
	})
	if err != nil {
		t.Fatal(err)
	}
	registryEndpointValue, err := json.Marshal(map[string]any{
		"apiVersion": "artifacts.kubephos.dev/v1alpha1", "kind": "RegistryEndpoint",
		"spec": map[string]any{"protocol": "oci", "host": "REGISTRY.EXAMPLE.TEST:5443", "url": "https://REGISTRY.EXAMPLE.TEST:5443", "caBundle": ca + ca, "insecure": false, "projects": []string{"development"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	registryCredentialValue := []byte(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"RegistryCredential","metadata":{"role":"push"},"spec":{"server":"REGISTRY.EXAMPLE.TEST:5443","username":"robot","password":"secret","project":"development","scopes":["pull","push"]}}`)
	values := memoryArtifactStore{
		"executor-endpoint":   executorEndpointValue,
		"executor-credential": executorCredentialValue,
		"registry-endpoint":   registryEndpointValue,
		"registry-credential": registryCredentialValue,
	}
	store := &memoryProfileStore{
		artifacts: map[string]domain.Artifact{
			"art_executor_endpoint":   testArtifact("art_executor_endpoint", "op_executor", "OCIExecutorEndpoint", false, "executor-endpoint", executorEndpointValue),
			"art_executor_credential": testArtifact("art_executor_credential", "op_executor", "OCIExecutorCredential", true, "executor-credential", executorCredentialValue),
			"art_registry_endpoint":   testArtifact("art_registry_endpoint", "op_registry", "RegistryEndpoint", false, "registry-endpoint", registryEndpointValue),
			"art_registry_credential": testArtifact("art_registry_credential", "op_registry", "RegistryCredential", true, "registry-credential", registryCredentialValue),
		},
		operations: map[string]domain.Operation{
			"op_executor": {ID: "op_executor", Status: domain.OperationSucceeded},
			"op_registry": {ID: "op_registry", Status: domain.OperationSucceeded},
		},
	}
	manager := NewManager(store, values, passthroughVault{}, nil)
	resolved, err := manager.resolveProfile(context.Background(), domain.PluginRuntimeProfile{
		EndpointArtifactID: "art_executor_endpoint", CredentialArtifactID: "art_executor_credential",
		Registries: []domain.PluginRuntimeRegistry{{EndpointArtifactID: "art_registry_endpoint", CredentialArtifactID: "art_registry_credential"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.host != "tcp://10.0.0.10:2376" || len(resolved.registries) != 1 || resolved.registries[0].Authority != "registry.example.test:5443" {
		t.Fatalf("unexpected resolved profile: %#v", resolved)
	}
	store.artifacts["art_registry_endpoint_two"] = testArtifact("art_registry_endpoint_two", "op_registry_two", "RegistryEndpoint", false, "registry-endpoint", registryEndpointValue)
	store.artifacts["art_registry_credential_two"] = testArtifact("art_registry_credential_two", "op_registry_two", "RegistryCredential", true, "registry-credential", registryCredentialValue)
	store.operations["op_registry_two"] = domain.Operation{ID: "op_registry_two", Status: domain.OperationSucceeded}
	if _, err := manager.resolveProfile(context.Background(), domain.PluginRuntimeProfile{
		EndpointArtifactID: "art_executor_endpoint", CredentialArtifactID: "art_executor_credential",
		Registries: []domain.PluginRuntimeRegistry{
			{EndpointArtifactID: "art_registry_endpoint", CredentialArtifactID: "art_registry_credential"},
			{EndpointArtifactID: "art_registry_endpoint_two", CredentialArtifactID: "art_registry_credential_two"},
		},
	}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("duplicate registry authority was accepted: %v", err)
	}
	store.operations["op_registry"] = domain.Operation{ID: "op_registry", Status: domain.OperationFailed}
	if _, err := manager.resolveProfile(context.Background(), domain.PluginRuntimeProfile{
		EndpointArtifactID: "art_executor_endpoint", CredentialArtifactID: "art_executor_credential",
		Registries: []domain.PluginRuntimeRegistry{{EndpointArtifactID: "art_registry_endpoint", CredentialArtifactID: "art_registry_credential"}},
	}); err == nil || !strings.Contains(err.Error(), "successful operation") {
		t.Fatalf("artifact from failed operation was accepted: %v", err)
	}
}

func testArtifact(id, operationID, artifactType string, sensitive bool, key string, value []byte) domain.Artifact {
	return domain.Artifact{
		ID: id, OperationID: operationID, Type: artifactType, Version: "v1alpha1", Sensitive: sensitive,
		StorageKey: key, Digest: artifacts.Digest(value), SizeBytes: int64(len(value)), StorageDigest: artifacts.Digest(value), StoredSizeBytes: int64(len(value)), VerifiedAt: time.Now(),
	}
}

func testIdentity(t *testing.T) (string, string, string) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Minute)
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KubePhos test CA"}, NotBefore: now, NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "KubePhos test client"}, NotBefore: now, NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	clientKeyDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}))
	key := string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyDER}))
	return ca, certificate, key
}
