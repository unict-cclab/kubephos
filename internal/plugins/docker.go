package plugins

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/id"
)

const containerOutputLimit = 1 << 20

type DockerRunner struct {
	host   string
	binary string
}

func NewDockerRunner(host string) ContainerRunner {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil
	}
	return &DockerRunner{host: host, binary: "docker"}
}

func (d *DockerRunner) Ready(ctx context.Context) error {
	if !strings.HasPrefix(d.host, "tcp://") {
		return errors.New("OCI plugin runner must use a dedicated TCP endpoint")
	}
	output, err := exec.CommandContext(ctx, d.binary, "--host", d.host, "info", "--format", "{{.ServerVersion}}").CombinedOutput()
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
	if _, err := validateOCIReference(image); err != nil {
		return nil, nil, err
	}
	name := strings.ReplaceAll(id.New("kubephos-plugin"), "_", "-")
	networkMode := "none"
	if network {
		networkMode = "bridge"
	}
	arguments := []string{
		"--host", d.host, "run", "--rm", "--name", name, "--pull=missing", "-i",
		"--user=65532:65532", "--network=" + networkMode, "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--memory=256m", "--cpus=0.5", "--pids-limit=64",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=64m", "--label=io.kubephos.runtime=plugin",
		image, command,
	}
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
	_ = exec.CommandContext(cleanupContext, d.binary, "--host", d.host, "rm", "-f", name).Run()
	if stdout.exceeded {
		return nil, messages, errors.New("plugin output exceeds 1 MiB")
	}
	if processErr != nil {
		return nil, messages, processErr
	}
	return stdout.Bytes(), messages, nil
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
