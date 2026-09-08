package main

import (
	"context"
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
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/engine"
	"kubephos.dev/kubephos/internal/plugins"
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
	case "version":
		fmt.Println(version)
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
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
	registry, err := plugins.LoadDirectory(configValue.PluginDirectory)
	if err != nil {
		return err
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	slog.Info("starting api", "address", configValue.HTTPAddress, "version", version)
	return api.Serve(ctx, configValue.HTTPAddress, api.NewServer(store, registry, artifactStore, version))
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
	registry, err := plugins.LoadDirectory(configValue.PluginDirectory)
	if err != nil {
		return err
	}
	artifactStore := artifacts.New(configValue.ArtifactEndpoint)
	slog.Info("starting worker", "instance", configValue.InstanceID, "concurrency", configValue.WorkerConcurrency)
	return engine.NewWorker(store, registry, artifactStore, configValue.InstanceID, configValue.WorkerConcurrency, configValue.WorkerPoll).Run(ctx)
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
	checks := []string{"/health/live", "/health/ready", "/api/v1/system"}
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
