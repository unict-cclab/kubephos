package auth

import (
	"strings"
	"testing"
)

func TestPasswordPolicy(t *testing.T) {
	for _, length := range []int{0, 11, 12, 128, 129} {
		err := ValidatePassword(strings.Repeat("p", length))
		if (err == nil) != (length >= 12 && length <= 128) {
			t.Fatalf("length %d: unexpected policy result %v", length, err)
		}
	}
}
