package artifacts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	endpoint string
	client   *http.Client
}

type Stored struct {
	Key    string
	Digest string
	Size   int64
}

func New(endpoint string) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), client: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/", nil)
	if err != nil {
		return err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 400 {
		return fmt.Errorf("artifact storage returned %s", response.Status)
	}
	return nil
}

func (c *Client) Put(ctx context.Context, key, filename, mediaType string, value []byte) (Stored, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return Stored{}, err
	}
	if _, err := part.Write(value); err != nil {
		return Stored{}, err
	}
	if err := writer.WriteField("Content-Type", mediaType); err != nil {
		return Stored{}, err
	}
	if err := writer.Close(); err != nil {
		return Stored{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+normalizeKey(key), &body)
	if err != nil {
		return Stored{}, err
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := c.client.Do(request)
	if err != nil {
		return Stored{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return Stored{}, fmt.Errorf("artifact upload returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	digest := sha256.Sum256(value)
	return Stored{Key: normalizeKey(key), Digest: "sha256:" + hex.EncodeToString(digest[:]), Size: int64(len(value))}, nil
}

func (c *Client) Get(ctx context.Context, key string) (io.ReadCloser, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+normalizeKey(key), nil)
	if err != nil {
		return nil, "", err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	if response.StatusCode != http.StatusOK {
		defer response.Body.Close()
		return nil, "", fmt.Errorf("artifact download returned %s", response.Status)
	}
	return response.Body, response.Header.Get("Content-Type"), nil
}

func (c *Client) ReadVerified(ctx context.Context, key, expectedDigest string, expectedSize int64) ([]byte, error) {
	reader, _, err := c.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	value, err := io.ReadAll(io.LimitReader(reader, MaxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if len(value) > MaxArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", MaxArtifactBytes)
	}
	if int64(len(value)) != expectedSize {
		return nil, fmt.Errorf("artifact size mismatch: expected %d, received %d", expectedSize, len(value))
	}
	digest := sha256.Sum256(value)
	actual := "sha256:" + hex.EncodeToString(digest[:])
	if actual != expectedDigest {
		return nil, fmt.Errorf("artifact digest mismatch: expected %s, received %s", expectedDigest, actual)
	}
	return value, nil
}

func OperationKey(operationID, filename string) string {
	return "/kubephos/operations/" + url.PathEscape(operationID) + "/" + url.PathEscape(filename)
}

func StepOutputKey(operationID, stepID, outputName string) string {
	return "/kubephos/operations/" + url.PathEscape(operationID) + "/steps/" + url.PathEscape(stepID) + "/" + url.PathEscape(outputName) + ".json"
}

func normalizeKey(key string) string {
	if strings.HasPrefix(key, "/") {
		return key
	}
	return "/" + key
}
