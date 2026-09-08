package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

type Vault struct {
	aead cipher.AEAD
}

func Open(path string) (*Vault, error) {
	key, err := loadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

func (v *Vault) Encrypt(value []byte) ([]byte, []byte, string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, "", err
	}
	ciphertext := v.aead.Seal(nil, nonce, value, nil)
	digest := sha256.Sum256(value)
	return nonce, ciphertext, hex.EncodeToString(digest[:6]), nil
}

func (v *Vault) Decrypt(nonce, ciphertext []byte) ([]byte, error) {
	value, err := v.aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, errors.New("credential could not be decrypted")
	}
	return value, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	value, err := os.ReadFile(path)
	if err == nil {
		return validateKey(value)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	value = make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return readKeyWithRetry(path)
	}
	if err != nil {
		return nil, err
	}
	if _, err := file.Write(value); err != nil {
		file.Close()
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	return value, nil
}

func readKeyWithRetry(path string) ([]byte, error) {
	var last error
	for attempt := 0; attempt < 50; attempt++ {
		value, err := os.ReadFile(path)
		if err == nil {
			value, err = validateKey(value)
		}
		if err == nil {
			return value, nil
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	return nil, last
}

func validateKey(value []byte) ([]byte, error) {
	if len(value) != 32 {
		return nil, fmt.Errorf("credential key must contain 32 bytes")
	}
	return value, nil
}
