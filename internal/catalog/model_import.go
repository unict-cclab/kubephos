package catalog

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var modelQuantizationFromName = regexp.MustCompile(`(?i)(IQ[0-9][A-Z0-9_]*|Q[0-9][A-Z0-9_]*)\.gguf$`)
var modelFilenameCharacters = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// ImportModel reads file metadata only; the experiment Job downloads model weights.
func ImportModel(ctx context.Context, source string, client *http.Client) (Model, error) {
	location, err := url.Parse(strings.TrimSpace(source))
	if err != nil || location.Hostname() == "" || (location.Scheme != "https" && location.Scheme != "http") || location.User != nil || location.Fragment != "" {
		return Model{}, fmt.Errorf("provide an absolute HTTP or HTTPS GGUF download URL")
	}
	if location.Hostname() == "huggingface.co" {
		parts := strings.Split(strings.Trim(location.Path, "/"), "/")
		if len(parts) >= 5 && parts[2] == "blob" {
			parts[2] = "resolve"
			location.Path = "/" + strings.Join(parts, "/")
			location.RawPath = ""
		}
	}

	if !strings.HasSuffix(strings.ToLower(location.Path), ".gguf") {
		return Model{}, fmt.Errorf("the URL must point to a GGUF file")
	}
	if regexp.MustCompile(`(?i)-[0-9]{5}-of-[0-9]{5}\.gguf$`).MatchString(location.Path) {
		return Model{}, fmt.Errorf("split GGUF files are not supported; select a single-file GGUF")
	}
	name := strings.TrimSuffix(path.Base(location.Path), path.Ext(location.Path))
	name = strings.Trim(modelFilenameCharacters.ReplaceAllString(name, "-"), "._-")
	if len(name) > 110 {
		name = name[:110]
	}
	if name == "" {
		name = "model"
	}
	quantization := "unknown"
	if match := modelQuantizationFromName.FindStringSubmatch(location.Path); match != nil {
		quantization = strings.ToUpper(match[1])
	}
	model := Model{ID: name, URL: location.String(), Backend: "llama-cpp", Quantization: quantization, Enabled: true}
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, model.URL, nil)
	if err != nil {
		return model, err
	}
	response, err := client.Do(request)
	if err != nil {
		var network net.Error
		var dns *net.DNSError
		var certificate *tls.CertificateVerificationError
		switch {
		case errors.As(err, &network) && network.Timeout():
			return model, errors.New("The model server did not respond in time. Try again or check the download URL.")
		case errors.As(err, &dns):
			return model, errors.New("Could not find the model server. Check the download URL.")
		case errors.As(err, &certificate):
			return model, errors.New("The model server's HTTPS certificate could not be verified.")
		default:
			return model, errors.New("Could not reach the model server. Check the download URL and try again.")
		}
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return model, fmt.Errorf("GGUF file is not accessible (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode >= 400 && response.StatusCode != http.StatusMethodNotAllowed && response.StatusCode != http.StatusNotImplemented {
		return model, fmt.Errorf("could not read model metadata (HTTP %d)", response.StatusCode)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		model.Size, _ = strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
		if model.Size < 0 {
			model.Size = 0
		}
	}
	if location.Hostname() == "huggingface.co" {
		parts := strings.Split(strings.Trim(location.Path, "/"), "/")
		if len(parts) < 5 || parts[2] != "resolve" {
			return model, fmt.Errorf("use a Hugging Face file URL with /resolve/, not /blob/")
		}
		hash := strings.Trim(response.Header.Get("X-Linked-Etag"), "\"")
		if !modelChecksumPattern.MatchString(hash) {
			hash = strings.Trim(response.Header.Get("Etag"), "\"")
		}
		size, _ := strconv.ParseInt(response.Header.Get("X-Linked-Size"), 10, 64)
		if size == 0 {
			size = model.Size
		}
		revision := response.Header.Get("X-Repo-Commit")
		if size <= 0 || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(revision) {
			return model, fmt.Errorf("Hugging Face did not return complete GGUF metadata")
		}
		parts[3] = revision
		location.Path = "/" + strings.Join(parts, "/")
		location.RawPath = ""
		location.RawQuery = ""
		model.URL = location.String()
		if !modelChecksumPattern.MatchString(hash) {
			hash = ""
		}
		model.SHA256 = hash
		model.Size = size
	}
	// Include the source identity to distinguish equal filenames from different hosts/revisions.
	identity := sha256.Sum256([]byte(model.URL))
	model.ID += "-" + hex.EncodeToString(identity[:4])
	if err := ValidateModel(model); err != nil {
		return model, err
	}
	return model, nil
}
