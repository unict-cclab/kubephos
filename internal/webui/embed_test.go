package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHandlerServesAssetsAndSPAFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("interface"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("asset"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := Handler(root)
	if err != nil {
		t.Fatal(err)
	}
	asset := httptest.NewRecorder()
	handler.ServeHTTP(asset, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || asset.Body.String() != "asset" || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("unexpected asset response: %d %q %q", asset.Code, asset.Body.String(), asset.Header().Get("Cache-Control"))
	}
	fallback := httptest.NewRecorder()
	handler.ServeHTTP(fallback, httptest.NewRequest(http.MethodGet, "/catalog", nil))
	if fallback.Code != http.StatusOK || fallback.Body.String() != "interface" || fallback.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected fallback response: %d %q %q", fallback.Code, fallback.Body.String(), fallback.Header().Get("Cache-Control"))
	}
}
