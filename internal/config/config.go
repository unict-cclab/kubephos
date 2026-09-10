package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddress       string
	DatabaseURL       string
	ArtifactEndpoint  string
	PluginDirectory   string
	CatalogDirectory  string
	WebDirectory      string
	CredentialKeyFile string
	PublicURL         string
	WorkerConcurrency int
	WorkerPoll        time.Duration
	InstanceID        string
	PluginRuntimeHost string
}

func Load() Config {
	return Config{
		HTTPAddress:       value("KUBEPHOS_HTTP_ADDRESS", ":8080"),
		DatabaseURL:       value("KUBEPHOS_DATABASE_URL", "postgres://kubephos:kubephos@localhost:5432/kubephos?sslmode=disable"),
		ArtifactEndpoint:  value("KUBEPHOS_ARTIFACT_ENDPOINT", "http://localhost:8888"),
		PluginDirectory:   value("KUBEPHOS_PLUGIN_DIRECTORY", "./plugins-dist"),
		CatalogDirectory:  value("KUBEPHOS_CATALOG_DIRECTORY", "./catalog/applications"),
		WebDirectory:      value("KUBEPHOS_WEB_DIRECTORY", "./frontend/dist"),
		CredentialKeyFile: value("KUBEPHOS_CREDENTIAL_KEY_FILE", "./.kubephos/master.key"),
		PublicURL:         value("KUBEPHOS_URL", "http://localhost:8080"),
		WorkerConcurrency: integer("KUBEPHOS_WORKER_CONCURRENCY", 4),
		WorkerPoll:        duration("KUBEPHOS_WORKER_POLL_INTERVAL", time.Second),
		InstanceID:        value("KUBEPHOS_INSTANCE_ID", "local"),
		PluginRuntimeHost: value("KUBEPHOS_PLUGIN_RUNTIME_HOST", ""),
	}
}

func value(key, fallback string) string {
	if current := os.Getenv(key); current != "" {
		return current
	}
	return fallback
}

func integer(key string, fallback int) int {
	current, err := strconv.Atoi(os.Getenv(key))
	if err != nil || current < 1 {
		return fallback
	}
	return current
}

func duration(key string, fallback time.Duration) time.Duration {
	current, err := time.ParseDuration(os.Getenv(key))
	if err != nil || current <= 0 {
		return fallback
	}
	return current
}
