package auth

import (
	"errors"
	"regexp"
)

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{2,39}$`)

func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("username must start with a lowercase letter and contain 3 to 40 lowercase letters, numbers, dots, dashes or underscores")
	}
	return nil
}
