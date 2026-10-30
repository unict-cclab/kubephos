package catalog

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

type modelTransport func(*http.Request) (*http.Response, error)

func (f modelTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestImportGGUFFromHuggingFaceURLOnly(t *testing.T) {
	hash := strings.Repeat("a", 64)
	revision := strings.Repeat("b", 40)
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodHead {
			t.Fatal("import must not download model weights")
		}
		return &http.Response{StatusCode: 302, Header: http.Header{"X-Linked-Etag": []string{"\"" + hash + "\""}, "X-Linked-Size": []string{"1234"}, "X-Repo-Commit": []string{revision}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	model, err := ImportModel(context.Background(), "https://huggingface.co/example/Model-GGUF/resolve/main/model-Q4_K_M.gguf", client)
	if err != nil {
		t.Fatal(err)
	}
	if model.Size != 1234 || model.SHA256 != hash || !strings.Contains(model.URL, "/resolve/"+revision+"/") || model.Quantization != "Q4_K_M" || !model.Enabled {
		t.Fatalf("incorrect imported metadata: %#v", model)
	}
}
func TestGenericURLDefersChecksumToDownloadJob(t *testing.T) {
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Length": []string{"4096"}, "Etag": []string{"not-a-sha256"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	model, err := ImportModel(context.Background(), "https://models.example.org/model-Q8_0.gguf", client)
	if err != nil {
		t.Fatal(err)
	}
	if model.Size != 4096 || model.SHA256 != "" || model.Quantization != "Q8_0" {
		t.Fatalf("incorrect metadata: %v", model)
	}
}
func TestModelURLValidationAndUnavailableFiles(t *testing.T) {
	client := &http.Client{Transport: modelTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 404, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	for _, source := range []string{"file:///tmp/model.gguf", "https://example.org/model.bin", "https://example.org/model-00001-of-00002.gguf", "https://example.org/missing.gguf"} {
		if _, err := ImportModel(context.Background(), source, client); err == nil {
			t.Fatalf("accepted invalid or missing file %s", source)
		}
	}
}

func TestModelConnectionErrorsDoNotExposeURLSecrets(t *testing.T) {
	for _, problem := range []error{context.DeadlineExceeded, &net.DNSError{Name: "internal-host", Err: "no such host"}, errors.New("transport failure at private-host")} {
		client := &http.Client{Transport: modelTransport(func(*http.Request) (*http.Response, error) { return nil, problem })}
		_, err := ImportModel(context.Background(), "https://models.example.test/model.gguf?access_token=private-token", client)
		if err == nil || strings.Contains(err.Error(), "private-token") || strings.Contains(err.Error(), "private-host") || strings.Contains(err.Error(), "internal-host") {
			t.Fatalf("unsafe or missing connection error: %v", err)
		}
	}
}
