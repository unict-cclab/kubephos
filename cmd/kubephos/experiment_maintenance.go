package main

import (
	"context"
	"errors"
	"fmt"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/storage"
	"os"
	"time"
)

func experimentMaintenance(configuration config.Config) error {
	if len(os.Args) < 3 {
		return errors.New("usage: kubephos maintenance <purge-deleted-experiments|retry-experiment-cleanup run-id>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	store, err := storage.Open(ctx, configuration.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	switch {
	case len(os.Args) == 3 && os.Args[2] == "purge-deleted-experiments":
		err = store.PurgeOrphanedExperimentHistory(ctx)
	case len(os.Args) == 4 && os.Args[2] == "retry-experiment-cleanup":
		err = store.RetryExperimentCleanup(ctx, os.Args[3])
	default:
		return errors.New("invalid experiment maintenance command")
	}
	if err == nil {
		fmt.Println("PASS", os.Args[2])
	}
	return err
}
