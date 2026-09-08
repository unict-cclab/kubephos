package plugins

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
)

type descriptor struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Version     string `yaml:"version"`
		Description string `yaml:"description"`
	} `yaml:"metadata"`
	Spec struct {
		Protocol            string         `yaml:"protocol"`
		Commands            []string       `yaml:"commands"`
		ConfigurationSchema map[string]any `yaml:"configurationSchema"`
		CredentialSchemas   []struct {
			Kind        string         `yaml:"kind"`
			Name        string         `yaml:"name"`
			Description string         `yaml:"description"`
			Schema      map[string]any `yaml:"schema"`
		} `yaml:"credentialSchemas"`
		Permissions  []string `yaml:"permissions"`
		Capabilities []string `yaml:"capabilities"`
		Runtime      struct {
			Executable string `yaml:"executable"`
			Timeout    string `yaml:"timeout"`
		} `yaml:"runtime"`
	} `yaml:"spec"`
}

type Process struct {
	manifest    Manifest
	executable  string
	timeout     time.Duration
	resolver    SecretResolver
	secretKinds map[string]bool
}

func LoadDirectory(directory string, resolver SecretResolver) (*Registry, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	values := []Plugin{}
	seen := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name(), "plugin.yaml")
		value, err := LoadProcess(path, resolver)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", path, err)
		}
		pluginID := value.Manifest().ID
		if seen[pluginID] {
			return nil, fmt.Errorf("plugin %q is declared more than once", pluginID)
		}
		seen[pluginID] = true
		values = append(values, value)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no plugins found in %s", directory)
	}
	return NewRegistry(values...), nil
}

func LoadProcess(path string, resolver SecretResolver) (*Process, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var definition descriptor
	if err := yaml.Unmarshal(value, &definition); err != nil {
		return nil, err
	}
	if definition.APIVersion != "plugins.kubephos.io/v1alpha1" || definition.Kind != "Plugin" || definition.Spec.Protocol != "v1alpha1" {
		return nil, errors.New("unsupported plugin contract")
	}
	if definition.Metadata.ID == "" || definition.Metadata.Name == "" || definition.Metadata.Version == "" {
		return nil, errors.New("plugin identity is incomplete")
	}
	required := []string{"validate", "plan", "precheck", "execute", "verify", "status", "cancel", "cleanup"}
	for _, command := range required {
		if !contains(definition.Spec.Commands, command) {
			return nil, fmt.Errorf("required command %q is missing", command)
		}
	}
	if definition.Spec.Runtime.Executable == "" {
		return nil, errors.New("plugin executable is missing")
	}
	executable := definition.Spec.Runtime.Executable
	if !filepath.IsAbs(executable) {
		executable = filepath.Join(filepath.Dir(path), executable)
	}
	info, err := os.Stat(executable)
	if err != nil {
		return nil, err
	}
	if info.Mode()&0111 == 0 {
		return nil, fmt.Errorf("plugin executable %s is not executable", executable)
	}
	schema, err := json.Marshal(definition.Spec.ConfigurationSchema)
	if err != nil {
		return nil, err
	}
	timeout := 30 * time.Minute
	if definition.Spec.Runtime.Timeout != "" {
		timeout, err = time.ParseDuration(definition.Spec.Runtime.Timeout)
		if err != nil || timeout <= 0 {
			return nil, errors.New("plugin runtime timeout is invalid")
		}
	}
	credentialSchemas := make([]CredentialSchema, 0, len(definition.Spec.CredentialSchemas))
	for _, value := range definition.Spec.CredentialSchemas {
		if value.Kind == "" || value.Name == "" {
			return nil, errors.New("credential schema identity is incomplete")
		}
		raw, err := json.Marshal(value.Schema)
		if err != nil {
			return nil, err
		}
		credentialSchemas = append(credentialSchemas, CredentialSchema{Kind: value.Kind, Name: value.Name, Description: value.Description, Schema: raw})
	}
	secretKinds := map[string]bool{}
	for _, permission := range definition.Spec.Permissions {
		if strings.HasPrefix(permission, "secrets.read:") {
			secretKinds[strings.TrimPrefix(permission, "secrets.read:")] = true
		}
	}
	plugin := &Process{
		manifest: Manifest{
			ID:                definition.Metadata.ID,
			Name:              definition.Metadata.Name,
			Version:           definition.Metadata.Version,
			Description:       definition.Metadata.Description,
			Schema:            schema,
			CredentialSchemas: credentialSchemas,
			Capabilities:      definition.Spec.Capabilities,
			Permissions:       definition.Spec.Permissions,
		},
		executable:  executable,
		timeout:     timeout,
		resolver:    resolver,
		secretKinds: secretKinds,
	}
	describeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var described Manifest
	if err := plugin.invoke(describeContext, "describe", map[string]any{}, &described, nil); err != nil {
		return nil, fmt.Errorf("describe plugin: %w", err)
	}
	if described.ID != plugin.manifest.ID || described.Version != plugin.manifest.Version {
		return nil, errors.New("plugin executable identity does not match its descriptor")
	}
	return plugin, nil
}

func (p *Process) Manifest() Manifest {
	return p.manifest
}

func (p *Process) Validate(ctx context.Context, raw json.RawMessage) domain.ValidationReport {
	var report domain.ValidationReport
	if err := p.invoke(ctx, "validate", raw, &report, nil); err != nil {
		return domain.ValidationReport{Valid: false, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "error", Path: "$", Message: err.Error()}}}
	}
	return report
}

func (p *Process) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	var plan domain.Plan
	err := p.invoke(ctx, "plan", raw, &plan, nil)
	return plan, err
}

func (p *Process) Precheck(ctx context.Context, step domain.PlanStep, log Logger) (domain.HealthReport, error) {
	var health domain.HealthReport
	err := p.invoke(ctx, "precheck", map[string]any{"step": step}, &health, log)
	return health, err
}

func (p *Process) Execute(ctx context.Context, step domain.PlanStep, log Logger) (json.RawMessage, error) {
	var result json.RawMessage
	err := p.invoke(ctx, "execute", map[string]any{"step": step}, &result, log)
	return result, err
}

func (p *Process) Verify(ctx context.Context, step domain.PlanStep, result json.RawMessage, log Logger) (domain.HealthReport, error) {
	var health domain.HealthReport
	err := p.invoke(ctx, "verify", map[string]any{"step": step, "result": result}, &health, log)
	return health, err
}

func (p *Process) invoke(parent context.Context, command string, input, output any, log Logger) error {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	inputPayload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	resolved, err := p.resolveSecrets(ctx, inputPayload)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"input": json.RawMessage(inputPayload), "secrets": resolved})
	if err != nil {
		return err
	}
	process := exec.CommandContext(ctx, p.executable, command)
	process.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	process.Stdout = &stdout
	stderr, err := process.StderrPipe()
	if err != nil {
		return err
	}
	lines := make(chan []string, 1)
	go scanLines(stderr, lines, log)
	if err := process.Start(); err != nil {
		return err
	}
	processErr := process.Wait()
	messages := <-lines
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if processErr != nil {
		message := strings.Join(messages, "; ")
		if message == "" {
			message = processErr.Error()
		}
		return errors.New(message)
	}
	if err := json.Unmarshal(stdout.Bytes(), output); err != nil {
		return fmt.Errorf("plugin returned invalid JSON: %w", err)
	}
	return nil
}

func (p *Process) resolveSecrets(ctx context.Context, payload []byte) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	if len(p.secretKinds) == 0 {
		return result, nil
	}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	collectCredentialRefs(value, refs)
	for credentialID := range refs {
		if p.resolver == nil {
			return nil, errors.New("credential resolver is unavailable")
		}
		kind, secret, err := p.resolver(ctx, credentialID)
		if err != nil {
			return nil, fmt.Errorf("resolve credential %s: %w", credentialID, err)
		}
		if !p.secretKinds[kind] {
			return nil, fmt.Errorf("plugin is not permitted to read credential kind %q", kind)
		}
		result[credentialID] = secret
	}
	return result, nil
}

func collectCredentialRefs(value any, result map[string]bool) {
	switch current := value.(type) {
	case map[string]any:
		for _, item := range current {
			collectCredentialRefs(item, result)
		}
	case []any:
		for _, item := range current {
			collectCredentialRefs(item, result)
		}
	case string:
		if strings.HasPrefix(current, "cred_") {
			result[current] = true
		}
	}
}

func scanLines(reader io.Reader, result chan<- []string, log Logger) {
	lines := []string{}
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if log != nil {
			_ = log("info", line)
		}
	}
	result <- lines
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
