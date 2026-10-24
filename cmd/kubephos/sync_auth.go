package main

import (
	"context"
	"errors"
	"fmt"
	"kubephos.dev/kubephos/internal/config"
	"os"
	"strings"
	"time"
)

func synchronizeAuthentication(cfg config.Config) error {
	if len(os.Args) != 2 {
		return errors.New("usage: kubephos sync-auth")
	}
	username := strings.TrimSpace(os.Getenv("KUBEPHOS_KEYCLOAK_ADMIN_USERNAME"))
	if username == "" {
		username = "kubephos-bootstrap"
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := configuredIdentity(cfg).SynchronizeConfiguration(ctx, username, os.Getenv("KUBEPHOS_KEYCLOAK_ADMIN_PASSWORD")); err != nil {
		return err
	}
	fmt.Println("Authentication configuration synchronized.")
	return nil
}
