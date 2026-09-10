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
	script := "#!/bin/sh\nif [ \"${10}\" = info ]; then\n  printf '%s' '[\"name=seccomp,profile=builtin\",\"name=rootless\"]'\nelif [ \"${10}\" = run ]; then\n  printf '%s\\n' \"$@\" > " + trace + "\n  cat >/dev/null\n  printf '%s' '{\"valid\":true}'\n  printf '%s\\n' 'runtime log' >&2\nfi\n"
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
	runner := &DockerRunner{host: "tcp://runtime:2376", binary: binary, ca: ca, cert: cert, key: key, allowedRegistries: map[string]bool{"registry.example.test": true}}
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
	runner := &DockerRunner{host: "tcp://runtime:2376", binary: "docker", allowedRegistries: map[string]bool{"example.test": true}}
	if err := runner.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("expected mutual TLS error, got %v", err)
	}
}

func TestDockerRunnerEnforcesRegistryPolicy(t *testing.T) {
	runner := NewDockerRunner("tcp://runtime:2376", "/ca", "/cert", "/key", "harbor.kubephos.test:5443")
	digest := strings.Repeat("a", 64)
	if err := ValidateRuntimeImage(runner, "harbor.kubephos.test:5443/team/plugin@sha256:"+digest); err != nil {
		t.Fatal(err)
	}
	for _, image := range []string{
		"other.test/team/plugin@sha256:" + digest,
		"harbor.kubephos.test.evil/team/plugin@sha256:" + digest,
		"harbor.kubephos.test:5443/team/plugin:latest",
	} {
		if err := ValidateRuntimeImage(runner, image); err == nil {
			t.Fatalf("expected image rejection for %s", image)
		}
	}
}

func TestDockerRunnerRequiresRegistryPolicy(t *testing.T) {
	runner := NewDockerRunner("tcp://runtime:2376", "/ca", "/cert", "/key")
	if err := ValidateRuntimeImage(runner, "example.test/plugin@sha256:"+strings.Repeat("a", 64)); err == nil || !strings.Contains(err.Error(), "at least one allowed registry") {
		t.Fatalf("expected missing policy error, got %v", err)
	}
}

func TestDockerRunnerRejectsRootfulExecutor(t *testing.T) {
	directory := t.TempDir()
	binary := filepath.Join(directory, "docker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' '[\"name=seccomp,profile=builtin\"]'\n"), 0755); err != nil {
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
	runner := &DockerRunner{host: "tcp://runtime:2376", binary: binary, ca: ca, cert: cert, key: key, allowedRegistries: map[string]bool{"registry.example.test": true}}
	if err := runner.Ready(context.Background()); err == nil || !strings.Contains(err.Error(), "rootless") {
		t.Fatalf("expected rootless executor error, got %v", err)
	}
}

func TestDockerRunnerStagesPrivateRegistryImageWithoutDaemonTrust(t *testing.T) {
	directory := t.TempDir()
	dockerTrace := filepath.Join(directory, "docker-arguments")
	skopeoTrace := filepath.Join(directory, "skopeo-arguments")
	docker := filepath.Join(directory, "docker")
	skopeo := filepath.Join(directory, "skopeo")
	dockerScript := "#!/bin/sh\ncase \"${10}\" in\n  info) printf '%s' '[\"name=rootless\"]' ;;\n  load) printf '%s' loaded ;;\n  image) if [ \"${11}\" = inspect ]; then printf '%s' sha256:image; fi ;;\n  run) printf '%s\\n' \"$@\" > " + dockerTrace + "; cat >/dev/null; printf '%s' '{\"valid\":true}' ;;\nesac\n"
	skopeoScript := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + skopeoTrace + "\n"
	if err := os.WriteFile(docker, []byte(dockerScript), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skopeo, []byte(skopeoScript), 0755); err != nil {
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
	runner := &DockerRunner{
		host: "tcp://runtime:2376", binary: docker, skopeo: skopeo, ca: ca, cert: cert, key: key,
		allowedRegistries: map[string]bool{"registry.example.test": true},
		registryAccess:    map[string]RegistryAccess{"registry.example.test": {Authority: "registry.example.test", CABundle: "test", Username: "robot", Password: "secret"}},
	}
	image := "registry.example.test/team/plugin@sha256:" + strings.Repeat("d", 64)
	if _, _, err := runner.Run(context.Background(), image, "describe", []byte(`{}`), false, nil); err != nil {
		t.Fatal(err)
	}
	skopeoArguments, err := os.ReadFile(skopeoTrace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(skopeoArguments), "docker://"+image) || !strings.Contains(string(skopeoArguments), "--src-cert-dir") || strings.Contains(string(skopeoArguments), "secret") {
		t.Fatalf("unexpected skopeo arguments: %s", skopeoArguments)
	}
	dockerArguments, err := os.ReadFile(dockerTrace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerArguments), "--pull=never") || !strings.Contains(string(dockerArguments), "kubephos.local/runtime/plugin-") || strings.Contains(string(dockerArguments), image) {
		t.Fatalf("private image was not staged safely: %s", dockerArguments)
	}
}
