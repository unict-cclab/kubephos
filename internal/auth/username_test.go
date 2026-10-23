package auth

import "testing"

func TestUsernamePolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{{"admin", true}, {"alice.smith", true}, {"user-1", true}, {"a", false}, {"Admin", false}, {"1user", false}, {"user name", false}} {
		if (ValidateUsername(tc.name) == nil) != tc.valid {
			t.Fatalf("unexpected username policy for %q", tc.name)
		}
	}
}
