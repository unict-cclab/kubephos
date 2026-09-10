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
	"reflect"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/schema"
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
		Provider            string         `yaml:"provider"`
		Commands            []string       `yaml:"commands"`
		ConfigurationSchema map[string]any `yaml:"configurationSchema"`
		CredentialSchemas   []struct {
			Kind        string         `yaml:"kind"`
			Name        string         `yaml:"name"`
			Description string         `yaml:"description"`
			Schema      map[string]any `yaml:"schema"`
		} `yaml:"credentialSchemas"`
		Artifacts struct {
			Inputs  []domain.ArtifactContract `yaml:"inputs"`
			Outputs []domain.ArtifactContract `yaml:"outputs"`
		} `yaml:"artifacts"`
		Permissions  []string `yaml:"permissions"`
		Capabilities []string `yaml:"capabilities"`
		Runtime      struct {
			Executable string `yaml:"executable"`
			Image      string `yaml:"image"`
			Timeout    string `yaml:"timeout"`
		} `yaml:"runtime"`
	} `yaml:"spec"`
}

type Process struct {
	manifest    Manifest
	executable  string
	image       string
	runner      ContainerRunner
	timeout     time.Duration
	resolver    SecretResolver
	connections ConnectionResolver
	catalog     CatalogResolver
	secretKinds map[string]bool
}

func LoadDirectory(directory string, resolver SecretResolver, connections ConnectionResolver, catalogResolver CatalogResolver, runners ...ContainerRunner) (*Registry, error) {
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
		value, err := LoadProcess(path, resolver, connections, catalogResolver, runners...)
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

func ValidatePackage(path string, runners ...ContainerRunner) (Manifest, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Manifest{}, err
	}
	if info.IsDir() {
		path = filepath.Join(path, "plugin.yaml")
	}
	plugin, err := LoadProcess(path, nil, nil, nil, runners...)
	if err != nil {
		return Manifest{}, err
	}
	return plugin.Manifest(), nil
}

func LoadProcess(path string, resolver SecretResolver, connections ConnectionResolver, catalogResolver CatalogResolver, runners ...ContainerRunner) (*Process, error) {
	value, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return loadDefinition(value, filepath.Dir(path), resolver, connections, catalogResolver, true, runners...)
}

func LoadDefinition(value []byte, resolver SecretResolver, connections ConnectionResolver, catalogResolver CatalogResolver, runners ...ContainerRunner) (*Process, error) {
	return loadDefinition(value, "", resolver, connections, catalogResolver, true, runners...)
}

func InspectDefinition(value []byte) (Manifest, error) {
	plugin, err := loadDefinition(value, "", nil, nil, nil, false)
	if err != nil {
		return Manifest{}, err
	}
	return plugin.Manifest(), nil
}

func loadDefinition(value []byte, baseDirectory string, resolver SecretResolver, connections ConnectionResolver, catalogResolver CatalogResolver, verify bool, runners ...ContainerRunner) (*Process, error) {
	var definition descriptor
	decoder := yaml.NewDecoder(bytes.NewReader(value))
	decoder.KnownFields(true)
	if err := decoder.Decode(&definition); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("plugin descriptor must contain one YAML document")
		}
		return nil, err
	}
	if definition.APIVersion != "plugins.kubephos.io/v1alpha1" || definition.Kind != "Plugin" || definition.Spec.Protocol != "v1alpha1" {
		return nil, errors.New("unsupported plugin contract")
	}
	if definition.Metadata.ID == "" || definition.Metadata.Name == "" || definition.Metadata.Version == "" {
		return nil, errors.New("plugin identity is incomplete")
	}
	for _, capability := range definition.Spec.Capabilities {
		if strings.HasPrefix(capability, "infrastructure.") && definition.Spec.Provider == "" {
			return nil, errors.New("infrastructure plugins must declare a provider")
		}
	}
	required := []string{"describe", "validate", "plan", "precheck", "execute", "verify", "status", "cancel", "cleanup"}
	for _, command := range required {
		if !contains(definition.Spec.Commands, command) {
			return nil, fmt.Errorf("required command %q is missing", command)
		}
	}
	if (definition.Spec.Runtime.Executable == "") == (definition.Spec.Runtime.Image == "") {
		return nil, errors.New("plugin runtime must declare exactly one executable or image")
	}
	executable := ""
	image := definition.Spec.Runtime.Image
	var runtimeManifest Runtime
	if definition.Spec.Runtime.Executable != "" {
		if baseDirectory == "" {
			return nil, errors.New("imported plugins must use an OCI image runtime")
		}
		executable = definition.Spec.Runtime.Executable
		if !filepath.IsAbs(executable) {
			executable = filepath.Join(baseDirectory, executable)
		}
		info, err := os.Stat(executable)
		if err != nil {
			return nil, err
		}
		if info.Mode()&0111 == 0 {
			return nil, fmt.Errorf("plugin executable %s is not executable", executable)
		}
		runtimeManifest = Runtime{Kind: "bundled"}
	} else {
		digest, err := validateOCIReference(image)
		if err != nil {
			return nil, err
		}
		if verify && (len(runners) == 0 || runners[0] == nil) {
			return nil, errors.New("OCI plugin runner is unavailable")
		}
		runtimeManifest = Runtime{Kind: "oci", Reference: image, Digest: digest}
	}
	schemaDefinition, err := json.Marshal(definition.Spec.ConfigurationSchema)
	if err != nil {
		return nil, err
	}
	if definition.Spec.ConfigurationSchema["type"] != "object" {
		return nil, errors.New("plugin configuration schema root must have type object")
	}
	if err := schema.ValidateDefinition(schemaDefinition); err != nil {
		return nil, fmt.Errorf("plugin configuration schema: %w", err)
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
		if value.Schema["type"] != "object" {
			return nil, fmt.Errorf("credential schema %q root must have type object", value.Kind)
		}
		if err := schema.ValidateDefinition(raw); err != nil {
			return nil, fmt.Errorf("credential schema %q: %w", value.Kind, err)
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
			Provider:          definition.Spec.Provider,
			Name:              definition.Metadata.Name,
			Version:           definition.Metadata.Version,
			Description:       definition.Metadata.Description,
			Schema:            schemaDefinition,
			CredentialSchemas: credentialSchemas,
			ArtifactInputs:    definition.Spec.Artifacts.Inputs,
			ArtifactOutputs:   definition.Spec.Artifacts.Outputs,
			Capabilities:      definition.Spec.Capabilities,
			Permissions:       definition.Spec.Permissions,
			Runtime:           runtimeManifest,
		},
		executable:  executable,
		image:       image,
		timeout:     timeout,
		resolver:    resolver,
		connections: connections,
		catalog:     catalogResolver,
		secretKinds: secretKinds,
	}
	if len(runners) > 0 {
		plugin.runner = runners[0]
	}
	if !verify {
		return plugin, nil
	}
	describeContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var described Manifest
	if err := plugin.invoke(describeContext, "describe", map[string]any{}, &described, nil); err != nil {
		return nil, fmt.Errorf("describe plugin: %w", err)
	}
	if described.ID != plugin.manifest.ID || described.Version != plugin.manifest.Version || described.Provider != plugin.manifest.Provider ||
		!reflect.DeepEqual(described.ArtifactInputs, plugin.manifest.ArtifactInputs) || !reflect.DeepEqual(described.ArtifactOutputs, plugin.manifest.ArtifactOutputs) {
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

func (p *Process) Cleanup(ctx context.Context, step domain.PlanStep, result json.RawMessage, log Logger) error {
	var response map[string]any
	return p.invoke(ctx, "cleanup", map[string]any{"step": step, "result": result}, &response, log)
}

func (p *Process) invoke(parent context.Context, command string, input, output any, log Logger) error {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	inputPayload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	referencePayload, err := referenceResolutionPayload(inputPayload)
	if err != nil {
		return err
	}
	connectionValues, err := p.resolveConnections(ctx, referencePayload)
	if err != nil {
		return err
	}
	catalogValues, err := p.resolveCatalog(ctx, referencePayload)
	if err != nil {
		return err
	}
	secretPayloads := []json.RawMessage{referencePayload}
	for _, configuration := range connectionValues {
		secretPayloads = append(secretPayloads, configuration)
	}
	secretInput, err := json.Marshal(secretPayloads)
	if err != nil {
		return err
	}
	resolved, err := p.resolveSecrets(ctx, secretInput)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{"input": json.RawMessage(inputPayload), "secrets": resolved, "connections": connectionValues, "catalog": catalogValues})
	if err != nil {
		return err
	}
	var stdout []byte
	var messages []string
	var processErr error
	if p.image != "" {
		stdout, messages, processErr = p.runner.Run(ctx, p.image, command, payload, contains(p.manifest.Permissions, "network.egress"), log)
	} else {
		process := exec.CommandContext(ctx, p.executable, command)
		process.Stdin = bytes.NewReader(payload)
		var output bytes.Buffer
		process.Stdout = &output
		stderr, err := process.StderrPipe()
		if err != nil {
			return err
		}
		lines := make(chan []string, 1)
		go scanLines(stderr, lines, log)
		if err := process.Start(); err != nil {
			return err
		}
		processErr = process.Wait()
		messages = <-lines
		stdout = output.Bytes()
	}
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
	if err := json.Unmarshal(stdout, output); err != nil {
		return fmt.Errorf("plugin returned invalid JSON: %w", err)
	}
	return nil
}

func validateOCIReference(reference string) (string, error) {
	parts := strings.Split(reference, "@sha256:")
	if len(parts) != 2 || parts[0] == "" || strings.ContainsAny(parts[0], " \t\r\n@") || len(parts[1]) != 64 {
		return "", errors.New("OCI plugin image must use an immutable sha256 digest")
	}
	for _, character := range parts[1] {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return "", errors.New("OCI plugin image has an invalid sha256 digest")
		}
	}
	return "sha256:" + parts[1], nil
}

func referenceResolutionPayload(payload []byte) ([]byte, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	if root, ok := value.(map[string]any); ok {
		delete(root, "result")
	}
	removeResolvedInputs(value)
	return json.Marshal(value)
}

func removeResolvedInputs(value any) {
	switch current := value.(type) {
	case map[string]any:
		delete(current, "resolvedInputs")
		for _, item := range current {
			removeResolvedInputs(item)
		}
	case []any:
		for _, item := range current {
			removeResolvedInputs(item)
		}
	}
}

func (p *Process) resolveCatalog(ctx context.Context, payload []byte) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	collectRefs(value, "app:", refs)
	if len(refs) > 0 && !contains(p.manifest.Permissions, "catalog.read:applications") {
		return nil, errors.New("plugin is not permitted to read catalog applications")
	}
	for applicationRef := range refs {
		if p.catalog == nil {
			return nil, errors.New("catalog resolver is unavailable")
		}
		descriptor, err := p.catalog(ctx, applicationRef)
		if err != nil {
			return nil, fmt.Errorf("resolve catalog application %s: %w", applicationRef, err)
		}
		result[applicationRef] = descriptor
	}
	return result, nil
}

func (p *Process) resolveConnections(ctx context.Context, payload []byte) (map[string]json.RawMessage, error) {
	result := map[string]json.RawMessage{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, err
	}
	refs := map[string]bool{}
	collectRefs(value, "conn_", refs)
	for connectionID := range refs {
		if p.connections == nil {
			return nil, errors.New("connection resolver is unavailable")
		}
		provider, configuration, err := p.connections(ctx, connectionID)
		if err != nil {
			return nil, fmt.Errorf("resolve connection %s: %w", connectionID, err)
		}
		if provider != p.manifest.Provider && !contains(p.manifest.Permissions, "connections.read:"+provider) {
			return nil, fmt.Errorf("plugin is not permitted to read provider connection %q", provider)
		}
		result[connectionID] = configuration
	}
	return result, nil
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
	collectRefs(value, "cred_", refs)
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

func collectRefs(value any, prefix string, result map[string]bool) {
	switch current := value.(type) {
	case map[string]any:
		for _, item := range current {
			collectRefs(item, prefix, result)
		}
	case []any:
		for _, item := range current {
			collectRefs(item, prefix, result)
		}
	case string:
		if strings.HasPrefix(current, prefix) {
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
