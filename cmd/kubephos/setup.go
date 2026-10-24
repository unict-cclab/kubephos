package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"os"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/auth"
	"kubephos.dev/kubephos/internal/config"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/keycloak"
	"kubephos.dev/kubephos/internal/storage"
)

type administratorInput struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

func readAdministrator(input io.Reader) (administratorInput, error) {
	var account administratorInput
	decoder := json.NewDecoder(io.LimitReader(input, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&account); err != nil {
		return account, errors.New("provide administrator username, email and password as JSON on standard input")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return account, errors.New("provide exactly one administrator JSON object")
	}
	account.Username = strings.ToLower(strings.TrimSpace(account.Username))
	account.Email = strings.ToLower(strings.TrimSpace(account.Email))
	address, emailErr := mail.ParseAddress(account.Email)
	if emailErr != nil || address.Address != account.Email {
		return account, errors.New("provide a valid administrator email")
	}
	if err := auth.ValidateUsername(account.Username); err != nil {
		return account, err
	}
	if err := auth.ValidatePassword(account.Password); err != nil {
		return account, err
	}
	return account, nil
}

// setupAdministrator runs before publishing the app port. Secrets arrive on
// stdin rather than process arguments, and existing accounts are never reset.
func setupAdministrator(cfg config.Config) error {
	if len(os.Args) != 2 {
		return errors.New("usage: kubephos setup-admin < credentials.json")
	}
	account, err := readAdministrator(os.Stdin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	store, err := storage.WaitForDatabase(ctx, cfg.DatabaseURL, 30*time.Second)
	if err != nil {
		return err
	}
	defer store.Close()
	count, err := store.IdentityUserCount(ctx)
	if err != nil {
		return err
	}
	if count != 0 {
		fmt.Println("Administrator setup already completed; existing accounts unchanged.")
		return nil
	}
	identity := configuredIdentity(cfg)
	externalID, err := identity.CreateUserWithTemporaryPassword(ctx, account.Username, account.Email, account.Password)
	if err != nil {
		return fmt.Errorf("create administrator in Keycloak: %w", err)
	}
	_, err = store.CreateInitialIdentityUser(ctx, domain.User{ID: id.New("usr"), Username: account.Username, ExternalID: externalID, Email: account.Email, Role: "admin", Enabled: true})
	if err != nil {
		if cleanupErr := identity.DeleteUser(ctx, externalID); cleanupErr != nil {
			return fmt.Errorf("save administrator: %w; could not remove the incomplete Keycloak account: %v", err, cleanupErr)
		}
		return fmt.Errorf("save administrator: %w", err)
	}
	fmt.Println("Initial administrator created.")
	return nil
}

func configuredIdentity(cfg config.Config) *keycloak.Client {
	return keycloak.New(keycloak.Config{
		InternalURL: cfg.KeycloakInternalURL, PublicURL: cfg.KeycloakPublicURL, ApplicationURL: cfg.PublicURL,
		Realm: cfg.KeycloakRealm, ClientID: cfg.KeycloakClientID, ClientSecret: cfg.KeycloakClientSecret,
		ManagementClientID: cfg.KeycloakManagementClientID, ManagementClientSecret: cfg.KeycloakManagementClientSecret,
	})
}
