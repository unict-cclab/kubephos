package plugins

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/id"
)

const containerOutputLimit = 1 << 20

type DockerRunner struct {
	host              string
	binary            string
	ca                string
	cert              string
	key               string
	allowedRegistries map[string]bool
}

var registryExpression = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`)

func NewDockerRunner(host, ca, cert, key string, registries ...string) ContainerRunner {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	allowedRegistries := map[string]bool{}
	for _, registry := range registries {
		registry = strings.ToLower(strings.TrimSpace(registry))
		if registry != "" {
			allowedRegistries[registry] = true
		}
	}
	return &DockerRunner{host: host, binary: "docker", ca: strings.TrimSpace(ca), cert: strings.TrimSpace(cert), key: strings.TrimSpace(key), allowedRegistries: allowedRegistries}
}

func (d *DockerRunner) Ready(ctx context.Context) error {
	if !strings.HasPrefix(d.host, "tcp://") {
		return errors.New("OCI plugin runner must use a dedicated TCP endpoint")
	}
	if err := d.validateRegistryPolicy(); err != nil {
		return err
	}
	if err := d.validateTLSFiles(); err != nil {
		return err
	}
	arguments := append(d.connectionArguments(), "info", "--format", "{{.ServerVersion}}")
	output, err := exec.CommandContext(ctx, d.binary, arguments...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	return nil
}

func (d *DockerRunner) Run(ctx context.Context, image, command string, payload []byte, network bool, log Logger) ([]byte, []string, error) {
	if err := d.Ready(ctx); err != nil {
		return nil, nil, err
	}
	if err := d.ValidateImage(image); err != nil {
		return nil, nil, err
	}
	name := strings.ReplaceAll(id.New("kubephos-plugin"), "_", "-")
	networkMode := "none"
	if network {
		networkMode = "bridge"
	}
	arguments := append(d.connectionArguments(),
		"run", "--rm", "--name", name, "--pull=missing", "-i",
		"--user=65532:65532", "--network="+networkMode, "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--memory=256m", "--cpus=0.5", "--pids-limit=64",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=64m", "--label=io.kubephos.runtime=plugin",
		image, command,
	)
	process := exec.CommandContext(ctx, d.binary, arguments...)
	process.Stdin = bytes.NewReader(payload)
	stdout := &limitedBuffer{limit: containerOutputLimit}
	process.Stdout = stdout
	stderr, err := process.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	lines := make(chan []string, 1)
	go scanLines(stderr, lines, log)
	if err := process.Start(); err != nil {
		return nil, nil, err
	}
	processErr := process.Wait()
	messages := <-lines
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(cleanupContext, d.binary, append(d.connectionArguments(), "rm", "-f", name)...).Run()
	if stdout.exceeded {
		return nil, messages, errors.New("plugin output exceeds 1 MiB")
	}
	if processErr != nil {
		return nil, messages, processErr
	}
	return stdout.Bytes(), messages, nil
}

func (d *DockerRunner) ValidateImage(image string) error {
	if _, err := validateOCIReference(image); err != nil {
		return err
	}
	if err := d.validateRegistryPolicy(); err != nil {
		return err
	}
	registry := imageRegistry(image)
	if !d.allowedRegistries[registry] {
		return fmt.Errorf("OCI plugin registry %q is not allowed", registry)
	}
	return nil
}

func (d *DockerRunner) validateRegistryPolicy() error {
	if len(d.allowedRegistries) == 0 {
		return errors.New("OCI plugin runtime requires at least one allowed registry")
	}
	for registry := range d.allowedRegistries {
		if !registryExpression.MatchString(registry) {
			return fmt.Errorf("OCI plugin registry %q is invalid", registry)
		}
		if separator := strings.LastIndexByte(registry, ':'); separator >= 0 {
			port, err := strconv.Atoi(registry[separator+1:])
			if err != nil || port < 1 || port > 65535 {
				return fmt.Errorf("OCI plugin registry %q has an invalid port", registry)
			}
		}
	}
	return nil
}

func imageRegistry(image string) string {
	name := strings.SplitN(image, "@", 2)[0]
	first := strings.SplitN(name, "/", 2)[0]
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first
	}
	return "docker.io"
}

func (d *DockerRunner) connectionArguments() []string {
	return []string{"--host", d.host, "--tlsverify", "--tlscacert", d.ca, "--tlscert", d.cert, "--tlskey", d.key}
}

func (d *DockerRunner) validateTLSFiles() error {
	values := []struct {
		name string
		path string
	}{{"CA", d.ca}, {"client certificate", d.cert}, {"client key", d.key}}
	for _, value := range values {
		if value.path == "" || !filepath.IsAbs(value.path) {
			return fmt.Errorf("OCI plugin runtime %s path must be absolute", value.name)
		}
		info, err := os.Stat(value.path)
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("OCI plugin runtime %s is unavailable", value.name)
		}
	}
	keyInfo, _ := os.Stat(d.key)
	if keyInfo.Mode().Perm()&0077 != 0 {
		return errors.New("OCI plugin runtime client key permissions are too broad")
	}
	return nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func (b *limitedBuffer) Write(value []byte) (int, error) {
	written := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.exceeded = true
		return written, nil
	}
	if len(value) > remaining {
		b.exceeded = true
		value = value[:remaining]
	}
	_, err := b.buffer.Write(value)
	return written, err
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buffer.Bytes()
}
