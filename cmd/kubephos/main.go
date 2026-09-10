package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"kubephos.dev/kubephos/internal/api"
	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/engine"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("kubephos stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	configValue := config.Load()
	switch command {
	case "serve":
		return serve(configValue)
	case "worker":
		return work(configValue)
	case "status":
		return status(configValue)
	case "doctor":
		return doctor(configValue)
	case "catalog":
		return catalogMaintenance(configValue)
	case "plugin":
		return pluginMaintenance(configValue)
	case "version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func pluginMaintenance(configValue config.Config) error {
	if len(os.Args) < 3 || len(os.Args) > 4 || os.Args[2] != "validate" {
		return errors.New("usage: kubephos plugin validate [path]")
	}
	path := "."
	if len(os.Args) == 4 {
		path = os.Args[3]
	}
	manifest, err := plugins.ValidatePackage(path, pluginRuntime(configValue))
	if err != nil {
		return err
	}
	fmt.Printf("PASS %s@%s (%s)\n", manifest.ID, manifest.Version, manifest.Name)
	return nil
}

func catalogMaintenance(configValue config.Config) error {
	if len(os.Args) != 3 || os.Args[2] != "validate" {
		return errors.New("usage: kubephos catalog validate")
	}
	applications, err := catalog.LoadDirectory(configValue.CatalogDirectory)
	if err != nil {
		return err
	}
	fmt.Printf("PASS %d application versions\n", len(applications))
	return nil
}

func serve(configValue config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 60*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		return err
	}
	applications, err := catalog.LoadDirectory(configValue.CatalogDirectory)
	if err != nil {
		return err
	}
	if err := store.SyncCatalogApplications(ctx, applications); err != nil {
		return err
	}
	vault, resolver, err := credentialRuntime(store, configValue.CredentialKeyFile)
	if err != nil {
		return err
	}
	runtime := pluginRuntime(configValue)
	registry, err := plugins.LoadDirectory(configValue.PluginDirectory, resolver, connectionRuntime(store), catalogRuntime(store), runtime)
	if err != nil {
		return err
	}
	loader := externalPluginLoader(resolver, connectionRuntime(store), catalogRuntime(store), runtime)
	if err := refreshInstalledPlugins(ctx, store, registry, loader); err != nil {
		slog.Error("load installed plugins", "error", err)
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	handler, err := api.NewServer(store, registry, artifactStore, vault, runtime, loader, version, configValue.WebDirectory)
	if err != nil {
		return err
	}
	slog.Info("starting api", "address", configValue.HTTPAddress, "version", version)
	return api.Serve(ctx, configValue.HTTPAddress, handler)
}

func work(configValue config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	store, err := storage.WaitForDatabase(ctx, configValue.DatabaseURL, 60*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Ready(ctx); err != nil {
		return err
	}
	vault, resolver, err := credentialRuntime(store, configValue.CredentialKeyFile)
	if err != nil {
		return err
	}
	runtime := pluginRuntime(configValue)
	if runtime != nil {
		readyContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		runtimeErr := runtime.Ready(readyContext)
		cancel()
		if runtimeErr != nil {
			return fmt.Errorf("OCI plugin runtime: %w", runtimeErr)
		}
	}
	registry, err := plugins.LoadDirectory(configValue.PluginDirectory, resolver, connectionRuntime(store), catalogRuntime(store), runtime)
	if err != nil {
		return err
	}
	loader := externalPluginLoader(resolver, connectionRuntime(store), catalogRuntime(store), runtime)
	if err := refreshInstalledPlugins(ctx, store, registry, loader); err != nil {
		return err
	}
	go watchInstalledPlugins(ctx, store, registry, loader)
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	slog.Info("starting worker", "instance", configValue.InstanceID, "concurrency", configValue.WorkerConcurrency)
	return engine.NewWorker(store, registry, artifactStore, vault, configValue.InstanceID, configValue.WorkerConcurrency, configValue.WorkerPoll).Run(ctx)
}

func externalPluginLoader(resolver plugins.SecretResolver, connections plugins.ConnectionResolver, catalogResolver plugins.CatalogResolver, runtime plugins.ContainerRunner) func([]byte) (plugins.Plugin, error) {
	return func(descriptor []byte) (plugins.Plugin, error) {
		return plugins.LoadDefinition(descriptor, resolver, connections, catalogResolver, runtime)
	}
}

func pluginRuntime(configValue config.Config) plugins.ContainerRunner {
	return plugins.NewDockerRunner(configValue.PluginRuntimeHost, configValue.PluginRuntimeCA, configValue.PluginRuntimeCert, configValue.PluginRuntimeKey, configValue.PluginRegistries...)
}

func refreshInstalledPlugins(ctx context.Context, store *storage.Store, registry *plugins.Registry, loader func([]byte) (plugins.Plugin, error)) error {
	packages, err := store.ListActivePluginPackages(ctx)
	if err != nil {
		return err
	}
	active := make(map[string]bool, len(packages))
	for _, pluginPackage := range packages {
		active[pluginPackage.PluginID] = true
		if registry.IsBundled(pluginPackage.PluginID) {
			return fmt.Errorf("installed plugin %q conflicts with a bundled plugin", pluginPackage.PluginID)
		}
		descriptorDigest := sha256.Sum256([]byte(pluginPackage.Descriptor))
		if pluginPackage.DescriptorDigest != "sha256:"+hex.EncodeToString(descriptorDigest[:]) {
			return fmt.Errorf("installed plugin %q descriptor integrity check failed", pluginPackage.PluginID)
		}
		current, currentErr := registry.Get(pluginPackage.PluginID)
		if currentErr == nil && current.Manifest().Matches(pluginPackage.Version, pluginPackage.Digest) {
			continue
		}
		plugin, err := loader([]byte(pluginPackage.Descriptor))
		if err != nil {
			return fmt.Errorf("load installed plugin %s: %w", pluginPackage.PluginID, err)
		}
		manifest := plugin.Manifest()
		if manifest.ID != pluginPackage.PluginID || !manifest.Matches(pluginPackage.Version, pluginPackage.Digest) {
			return fmt.Errorf("installed plugin %q metadata does not match its descriptor", pluginPackage.PluginID)
		}
		if err := registry.Install(plugin); err != nil {
			return err
		}
	}
	for _, pluginID := range registry.ExternalIDs() {
		if !active[pluginID] {
			if err := registry.Remove(pluginID); err != nil {
				return err
			}
		}
	}
	return nil
}

func watchInstalledPlugins(ctx context.Context, store *storage.Store, registry *plugins.Registry, loader func([]byte) (plugins.Plugin, error)) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := refreshInstalledPlugins(ctx, store, registry, loader); err != nil {
				slog.Error("refresh installed plugins", "error", err)
			}
		}
	}
}

func credentialRuntime(store *storage.Store, keyFile string) (*secrets.Vault, plugins.SecretResolver, error) {
	vault, err := secrets.Open(keyFile)
	if err != nil {
		return nil, nil, err
	}
	resolver := func(ctx context.Context, credentialID string) (string, json.RawMessage, error) {
		credential, err := store.GetEncryptedCredential(ctx, credentialID)
		if err != nil {
			return "", nil, err
		}
		value, err := vault.Decrypt(credential.Nonce, credential.Ciphertext)
		if err != nil {
			return "", nil, err
		}
		if !json.Valid(value) {
			return "", nil, errors.New("credential payload is invalid")
		}
		return credential.Kind, value, nil
	}
	return vault, resolver, nil
}

func connectionRuntime(store *storage.Store) plugins.ConnectionResolver {
	return func(ctx context.Context, connectionID string) (string, json.RawMessage, error) {
		connection, err := store.GetProviderConnectionConfiguration(ctx, connectionID)
		if err != nil {
			return "", nil, err
		}
		return connection.Provider, connection.Configuration, nil
	}
}

func catalogRuntime(store *storage.Store) plugins.CatalogResolver {
	return func(ctx context.Context, reference string) (json.RawMessage, error) {
		applicationID, version, err := catalog.ParseReference(reference)
		if err != nil {
			return nil, err
		}
		return store.GetCatalogApplicationDescriptor(ctx, applicationID, version)
	}
}

func status(configValue config.Config) error {
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get(configValue.PublicURL + "/api/v1/system")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("api returned %s", response.Status)
	}
	var value any
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		return err
	}
	output, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(output))
	return nil
}

func doctor(configValue config.Config) error {
	client := &http.Client{Timeout: 5 * time.Second}
	checks := []string{"/health/live", "/health/ready", "/api/v1/auth/status"}
	failed := false
	for _, path := range checks {
		response, err := client.Get(configValue.PublicURL + path)
		if err != nil {
			fmt.Printf("FAIL %s: %v\n", path, err)
			failed = true
			continue
		}
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			fmt.Printf("FAIL %s: %s\n", path, response.Status)
			failed = true
			continue
		}
		fmt.Printf("PASS %s\n", path)
	}
	if failed {
		return errors.New("one or more checks failed")
	}
	return nil
}
