package plugins

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerRunnerAppliesIsolation(t *testing.T) {
	directory := t.TempDir()
	trace := filepath.Join(directory, "arguments")
	binary := filepath.Join(directory, "docker")
	script := "#!/bin/sh\nif [ \"${10}\" = run ]; then\n  printf '%s\\n' \"$@\" > " + trace + "\n  cat >/dev/null\n  printf '%s' '{\"valid\":true}'\n  printf '%s\\n' 'runtime log' >&2\nfi\n"
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	ca := filepath.Join(directory, "ca.pem")
	cert := filepath.Join(directory, "cert.pem")
	key := filepath.Join(directory, "key.pem")
	for _, path := range []string{ca, cert, key} {
		if err := os.WriteFile(path, []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	runner := &DockerRunner{host: "tcp://runtime:2376", binary: binary, ca: ca, cert: cert, key: key}
	logs := []string{}
	digest := strings.Repeat("b", 64)
	output, _, err := runner.Run(context.Background(), "registry.example.test/plugin@sha256:"+digest, "describe", []byte(`{}`), false, func(_, message string) error {
		logs = append(logs, message)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	values := string(arguments)
	for _, expected := range []string{"--tlsverify", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65532:65532", "--memory=256m", "--cpus=0.5", "--pids-limit=64"} {
		if !strings.Contains(values, expected) {
			t.Fatalf("missing %s in %s", expected, values)
		}
	}
	if string(output) != `{"valid":true}` || len(logs) != 1 || logs[0] != "runtime log" {
		t.Fatalf("unexpected output or logs: %s %#v", output, logs)
	}
}

func TestDockerRunnerRejectsHostSocket(t *testing.T) {
	runner := &DockerRunner{host: "unix:///var/run/docker.sock", binary: "docker"}
	if _, _, err := runner.Run(context.Background(), "example.test/plugin@sha256:"+strings.Repeat("c", 64), "describe", nil, false, nil); err == nil {
		t.Fatal("expected dedicated endpoint error")
	}
}

func TestDockerRunnerRequiresMutualTLS(t *testing.T) {
	runner := &DockerRunner{host: "tcp://runtime:2376", binary: "docker"}
	if err := runner.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("expected mutual TLS error, got %v", err)
	}
}
