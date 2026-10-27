package mesh

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/kubeconfig"
)

const cliVersion = "v0.20.1"

type cliRunner struct{ directory, binary string }

func (r *cliRunner) Close() {
	if r.directory != "" {
		os.RemoveAll(r.directory)
	}
}
func (r *cliRunner) Run(ctx context.Context, configs map[string]string, args ...string) (string, error) {
	if r.binary == "" {
		if err := r.install(ctx); err != nil {
			return "", err
		}
	}
	config, err := MergedKubeconfig(configs)
	if err != nil {
		return "", err
	}
	path := filepath.Join(r.directory, "kubeconfig")
	if err := os.WriteFile(path, config, 0600); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, r.binary, append([]string{"--kubeconfig", path}, args...)...)
	// Cilium's destination clients load the default kubeconfig instead of the
	// root flag. Give every client the same private, merged configuration.
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "KUBECONFIG=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "KUBECONFIG="+path)
	result, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("Cilium command failed: %s", strings.TrimSpace(string(result)))
	}
	return string(result), nil
}
func MergedKubeconfig(configs map[string]string) ([]byte, error) {
	names := make([]string, 0, len(configs))
	for name := range configs {
		names = append(names, name)
	}
	sort.Strings(names)
	merged := map[string]any{"apiVersion": "v1", "kind": "Config", "clusters": []any{}, "users": []any{}, "contexts": []any{}}
	for _, name := range names {
		var config map[string]any
		if err := yaml.Unmarshal([]byte(configs[name]), &config); err != nil {
			return nil, err
		}
		for _, key := range []string{"clusters", "users", "contexts"} {
			items, ok := config[key].([]any)
			if !ok {
				return nil, errors.New("member kubeconfig identity is incomplete")
			}
			merged[key] = append(merged[key].([]any), items...)
		}
		if merged["current-context"] == nil {
			merged["current-context"] = config["current-context"]
		}
	}
	return yaml.Marshal(merged)
}
func (r *cliRunner) install(ctx context.Context) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return errors.New("Cilium CLI requires a Linux amd64 or arm64 runtime")
	}
	directory, err := os.MkdirTemp("", "kubephos-cilium-*")
	if err != nil {
		return err
	}
	r.directory = directory
	archive := "cilium-linux-" + runtime.GOARCH + ".tar.gz"
	base := "https://github.com/cilium/cilium-cli/releases/download/" + cliVersion + "/" + archive
	client := &http.Client{Timeout: 90 * time.Second}
	download := func(url string, limit int64) ([]byte, error) {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		response, err := client.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, fmt.Errorf("Cilium CLI download returned HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if int64(len(data)) > limit {
			return nil, errors.New("Cilium CLI download exceeds size limit")
		}
		return data, err
	}
	sums, err := download(base+".sha256sum", 4096)
	if err != nil {
		return err
	}
	fields := strings.Fields(string(sums))
	if len(fields) < 1 {
		return errors.New("Cilium CLI checksum unavailable")
	}
	raw, err := download(base, 100<<20)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != fields[0] {
		return errors.New("Cilium CLI checksum mismatch")
	}
	file := filepath.Join(directory, archive)
	if err := os.WriteFile(file, raw, 0600); err != nil {
		return err
	}
	input, err := os.Open(file)
	if err != nil {
		return err
	}
	defer input.Close()
	compressed, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		entry, err := reader.Next()
		if err != nil {
			return errors.New("Cilium CLI executable is missing")
		}
		if entry.Name != "cilium" {
			continue
		}
		if entry.Typeflag != tar.TypeReg || entry.Size > 200<<20 {
			return errors.New("invalid Cilium CLI archive")
		}
		path := filepath.Join(directory, "cilium")
		output, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, io.LimitReader(reader, 200<<20))
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		r.binary = path
		return nil
	}
}

func (r *cliRunner) Kubectl(ctx context.Context, config string, stdin []byte, args ...string) (string, error) {
	raw, err := kubeconfig.RunKubectl(ctx, config, stdin, args...)
	return string(raw), err
}
