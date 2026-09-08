package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadVerifiedChecksDigestAndSize(t *testing.T) {
	value := []byte(`{"healthy":true}`)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write(value)
	}))
	defer server.Close()
	digest := sha256.Sum256(value)
	expected := "sha256:" + hex.EncodeToString(digest[:])
	client := New(server.URL)
	actual, err := client.ReadVerified(context.Background(), "/artifact", expected, int64(len(value)))
	if err != nil {
		t.Fatal(err)
	}
	if string(actual) != string(value) {
		t.Fatalf("unexpected content %s", actual)
	}
	if _, err := client.ReadVerified(context.Background(), "/artifact", "sha256:invalid", int64(len(value))); err == nil {
		t.Fatal("expected digest mismatch")
	}
	if _, err := client.ReadVerified(context.Background(), "/artifact", expected, int64(len(value)+1)); err == nil {
		t.Fatal("expected size mismatch")
	}
}
