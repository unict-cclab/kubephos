package secrets

import (
	"bytes"
	"path/filepath"
	"testing"
)

func TestVaultPersistsKeyAndDecrypts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ciphertext, fingerprint, err := first.Encrypt([]byte(`{"token":"secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, []byte("secret")) {
		t.Fatal("ciphertext contains plaintext")
	}
	if len(fingerprint) != 12 {
		t.Fatalf("unexpected fingerprint length %d", len(fingerprint))
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	value, err := second.Decrypt(nonce, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != `{"token":"secret"}` {
		t.Fatalf("unexpected plaintext %s", value)
	}
}
