package auth

import "errors"

// ValidatePassword preserves the platform policy; Keycloak hashes and stores
// passwords. Validation must not generate a local hash that is never used.
func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 128 {
		return errors.New("password must contain between 12 and 128 characters")
	}
	return nil
}
