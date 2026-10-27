package transfer

import (
	"bytes"
	"errors"
	"testing"
)

func TestProtectedStateArchive(t *testing.T) {
	state := &State{Format: 1, Schema: "schema", Tables: map[string][]Row{"credentials": {{"id": []byte(`"cred_one"`)}}}, Credentials: map[string][]byte{"cred_one": []byte(`{"token":"never-in-clear"}`)}}
	password := "test-archive-password"
	first, err := Encrypt(state, password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Encrypt(state, password)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first, second) || bytes.Contains(first, []byte("never-in-clear")) {
		t.Fatal("archive leaks secret or reuses encryption randomness")
	}
	result, err := Decrypt(first, password)
	if err != nil || !bytes.Equal(result.Credentials["cred_one"], state.Credentials["cred_one"]) {
		t.Fatal("roundtrip failed", err)
	}
	if _, err = Decrypt(first, "incorrect-password"); !errors.Is(err, ErrArchive) {
		t.Fatal("wrong password accepted", err)
	}
	for _, position := range []int{0, len(magic), len(magic) + 16, len(first) - 1} {
		altered := bytes.Clone(first)
		altered[position] ^= 1
		if _, err = Decrypt(altered, password); err == nil {
			t.Fatal("tampered archive accepted")
		}
	}
	for _, input := range [][]byte{nil, first[:8], first[:len(first)-1]} {
		if _, err = Decrypt(input, password); err == nil {
			t.Fatal("truncated archive accepted")
		}
	}
	if _, err = Encrypt(state, "short"); err == nil {
		t.Fatal("weak archive password accepted")
	}
}
