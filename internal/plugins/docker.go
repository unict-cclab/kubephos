package plugins

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/id"
)

const containerOutputLimit = 1 << 20

type DockerRunner struct {
	host              string
	binary            string
	skopeo            string
	ca                string
	cert              string
	key               string
	allowedRegistries map[string]bool
	registryAccess    map[string]RegistryAccess
}

type RegistryAccess struct {
	Authority string
	CABundle  string
	Username  string
	Password  string
}

var registryExpression = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`)

func NewDockerRunner(host, ca, cert, key string, registries ...string) ContainerRunner {
	return NewDockerRunnerWithAccess(host, ca, cert, key, registries, nil)
}

func NewDockerRunnerWithAccess(host, ca, cert, key string, registries []string, access []RegistryAccess) ContainerRunner {
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
	registryAccess := map[string]RegistryAccess{}
	for _, current := range access {
		authority := strings.ToLower(strings.TrimSpace(current.Authority))
		if authority != "" {
			current.Authority = authority
			registryAccess[authority] = current
		}
	}
	return &DockerRunner{host: host, binary: "docker", skopeo: "skopeo", ca: strings.TrimSpace(ca), cert: strings.TrimSpace(cert), key: strings.TrimSpace(key), allowedRegistries: allowedRegistries, registryAccess: registryAccess}
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
	arguments := append(d.connectionArguments(), "info", "--format", "{{json .SecurityOptions}}")
	output, err := limitedCommand(ctx, d.binary, arguments...)
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errors.New(message)
	}
	var securityOptions []string
	if err := json.Unmarshal(bytes.TrimSpace(output), &securityOptions); err != nil {
		return errors.New("OCI plugin executor returned invalid security information")
	}
	rootless := false
	for _, option := range securityOptions {
		if option == "name=rootless" || option == "rootless" {
			rootless = true
			break
		}
	}
	if !rootless {
		return errors.New("OCI plugin executor must run in rootless mode")
	}
	return nil
}

func (d *DockerRunner) State(ctx context.Context) (string, string) {
	if err := d.Ready(ctx); err != nil {
		return "unavailable", err.Error()
	}
	return "healthy", "The dedicated OCI executor is ready."
}

func (d *DockerRunner) Environment(ctx context.Context) ([]string, func(), error) {
	if err := d.Ready(ctx); err != nil {
		return nil, func() {}, err
	}
	registries := make([]string, 0, len(d.allowedRegistries))
	for registry := range d.allowedRegistries {
		registries = append(registries, registry)
	}
	sort.Strings(registries)
	return []string{
		"KUBEPHOS_PLUGIN_RUNTIME_HOST=" + d.host,
		"KUBEPHOS_PLUGIN_RUNTIME_CA=" + d.ca,
		"KUBEPHOS_PLUGIN_RUNTIME_CERT=" + d.cert,
		"KUBEPHOS_PLUGIN_RUNTIME_KEY=" + d.key,
		"KUBEPHOS_PLUGIN_ALLOWED_REGISTRIES=" + strings.Join(registries, ","),
	}, func() {}, nil
}

func (d *DockerRunner) Run(ctx context.Context, image, command string, payload []byte, network bool, log Logger) ([]byte, []string, error) {
	if err := d.Ready(ctx); err != nil {
		return nil, nil, err
	}
	if err := d.ValidateImage(image); err != nil {
		return nil, nil, err
	}
	runtimeImage, removeImage, err := d.prepareImage(ctx, image)
	if err != nil {
		return nil, nil, err
	}
	defer removeImage()
	name := strings.ReplaceAll(id.New("kubephos-plugin"), "_", "-")
	networkMode := "none"
	if network {
		networkMode = "bridge"
	}
	pullPolicy := "missing"
	if runtimeImage != image {
		pullPolicy = "never"
	}
	arguments := append(d.connectionArguments(),
		"run", "--rm", "--name", name, "--pull="+pullPolicy, "-i",
		"--user=65532:65532", "--network="+networkMode, "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--memory=256m", "--cpus=0.5", "--pids-limit=64",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=64m", "--label=io.kubephos.runtime=plugin",
		runtimeImage, command,
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
	if err := process.Start(); err != nil {
		return nil, nil, err
	}
	go scanLines(stderr, lines, log)
	messages := <-lines
	processErr := process.Wait()
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

func (d *DockerRunner) prepareImage(ctx context.Context, image string) (string, func(), error) {
	access, managed := d.registryAccess[imageRegistry(image)]
	if !managed {
		return image, func() {}, nil
	}
	if strings.TrimSpace(access.CABundle) == "" || access.Username == "" || access.Password == "" {
		return "", func() {}, errors.New("managed OCI registry access is incomplete")
	}
	root, err := os.MkdirTemp("", "kubephos-oci-image-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(root) }
	certificates := filepath.Join(root, "certificates")
	if err := os.Mkdir(certificates, 0700); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := os.WriteFile(filepath.Join(certificates, "ca.crt"), []byte(access.CABundle), 0600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	authValue, err := json.Marshal(map[string]any{"auths": map[string]any{access.Authority: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte(access.Username + ":" + access.Password))}}})
	if err != nil {
		cleanup()
		return "", func() {}, err
	}
	authFile := filepath.Join(root, "auth.json")
	if err := os.WriteFile(authFile, authValue, 0600); err != nil {
		cleanup()
		return "", func() {}, err
	}
	digest := strings.TrimPrefix(strings.SplitN(image, "@", 2)[1], "sha256:")
	localReference := "kubephos.local/runtime/plugin-" + digest[:16] + "-" + strings.ReplaceAll(id.New("run"), "_", "-") + ":sealed"
	archive := filepath.Join(root, "image.tar")
	copyArguments := []string{"copy", "--retry-times", "3", "--authfile", authFile, "--src-cert-dir", certificates, "docker://" + image, "docker-archive:" + archive + ":" + localReference}
	skopeo := d.skopeo
	if skopeo == "" {
		skopeo = "skopeo"
	}
	if output, err := limitedCommand(ctx, skopeo, copyArguments...); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("stage OCI plugin image: %s", commandMessage(output, err))
	}
	if output, err := limitedCommand(ctx, d.binary, append(d.connectionArguments(), "load", "--input", archive)...); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("load OCI plugin image: %s", commandMessage(output, err))
	}
	remove := func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = limitedCommand(cleanupContext, d.binary, append(d.connectionArguments(), "image", "rm", "-f", localReference)...)
		cleanup()
	}
	if output, err := limitedCommand(ctx, d.binary, append(d.connectionArguments(), "image", "inspect", localReference, "--format", "{{.Id}}")...); err != nil || strings.TrimSpace(string(output)) == "" {
		remove()
		return "", func() {}, errors.New("staged OCI plugin image is unavailable on the executor")
	}
	return localReference, remove, nil
}

func limitedCommand(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, arguments...)
	output := &limitedBuffer{limit: containerOutputLimit}
	command.Stdout = output
	command.Stderr = output
	err := command.Run()
	if output.exceeded && err == nil {
		err = errors.New("command output exceeds 1 MiB")
	}
	return output.Bytes(), err
}

func commandMessage(output []byte, err error) string {
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	if len(message) > 4096 {
		message = message[len(message)-4096:]
	}
	return message
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
