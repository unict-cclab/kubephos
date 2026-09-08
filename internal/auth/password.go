package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const memory = 64 * 1024
const iterations = 3
const parallelism = 2
const saltLength = 16
const keyLength = 32

func HashPassword(password string) (string, error) {
	if len(password) < 12 || len(password) > 128 {
		return "", errors.New("password must contain between 12 and 128 characters")
	}
	salt := make([]byte, saltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	digest := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, keyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, iterations, parallelism, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(digest)), nil
}

func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var parsedMemory uint32
	var parsedIterations uint32
	var parsedParallelism uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &parsedMemory, &parsedIterations, &parsedParallelism); err != nil {
		return false
	}
	if parsedMemory > memory || parsedIterations > iterations || parsedParallelism > parallelism || parsedMemory < 8*1024 || parsedIterations < 1 || parsedParallelism < 1 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 || len(salt) > 32 {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != keyLength {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, parsedIterations, parsedMemory, parsedParallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}
