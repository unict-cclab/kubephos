package nfsbrowser

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
)

const (
	pluginID          = "io.kubephos.storage.nfs.browser"
	artifactAPI       = "artifacts.kubephos.dev/v1alpha1"
	managedExport     = "/srv/kubephos"
	managedMarker     = "/var/lib/kubephos/nfs.marker"
	readSizeLimit     = 512 << 10
	writeSizeLimit    = 128 << 10
	listingFieldLimit = 2000
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage
}

type Spec struct {
	MachineSetRef            string
	MachineAccessRef         string
	SharedStorageEndpointRef string
	Action                   string
	Path                     string
	Content                  string
	ProtectOutput            bool
}

type machineSet struct {
	APIVersion string
	Kind       string
	Spec       struct {
		NetworkCIDR string
		Machines    []machine
	}
}

type machine struct {
	ID      string
	Name    string
	Address string
	SSHPort int
	SSHUser string
	State   string
}

type machineAccess struct {
	APIVersion string
	Kind       string
	Spec       struct {
		Algorithm  string
		PublicKey  string
		PrivateKey string
	}
}

type sharedStorageEndpoint struct {
	APIVersion string
	Kind       string
	Spec       struct {
		Protocol   string
		Server     string
		ExportPath string
		ClientCIDR string
	}
}

type fileEntry struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	SizeBytes    int64   `json:"sizeBytes"`
	ModifiedUnix float64 `json:"modifiedUnix"`
}

type storageViewSpec struct {
	Action        string      `json:"action"`
	Server        string      `json:"server"`
	ExportPath    string      `json:"exportPath"`
	Path          string      `json:"path"`
	State         string      `json:"state"`
	Entries       []fileEntry `json:"entries,omitempty"`
	Truncated     bool        `json:"truncated,omitempty"`
	ContentBase64 string      `json:"contentBase64,omitempty"`
	SizeBytes     int64       `json:"sizeBytes,omitempty"`
	Digest        string      `json:"digest,omitempty"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Provider: "nfs", Name: "NFS data browser", Version: "0.1.0",
		Description:     "Lists, reads, writes and deletes bounded files inside a verified managed NFS export.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","sharedStorageEndpointRef","action","path","content","protectOutput"],"properties":{"machineSetRef":{"type":"string","title":"Storage machine","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"sharedStorageEndpointRef":{"type":"string","title":"Managed NFS export","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"SharedStorageEndpoint","x-kubephos-artifact-version":"v1alpha1"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["list","read","write","delete"],"default":"list"},"path":{"type":"string","title":"Relative path","description":"Dot selects the export root; parent traversal and absolute paths are rejected.","minLength":1,"maxLength":512,"default":"."},"content":{"type":"string","title":"File content","description":"Used only by write and limited to 128 KiB.","maxLength":131072,"default":"","x-kubephos-multiline":true},"protectOutput":{"type":"boolean","title":"Protect result","description":"Keep enabled when names or file content may be private.","default":true}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}, {Type: "SharedStorageEndpoint", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "SharedStorageView", Version: "v1alpha1"}},
		Capabilities:    []string{"infrastructure.storage.control", "infrastructure.storage.preflight"},
		Permissions:     []string{"network.ssh", "storage.read", "storage.manage"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	refs := []struct{ path, value string }{{"machineSetRef", spec.MachineSetRef}, {"machineAccessRef", spec.MachineAccessRef}, {"sharedStorageEndpointRef", spec.SharedStorageEndpointRef}}
	seen := map[string]bool{}
	for _, ref := range refs {
		if !strings.HasPrefix(ref.value, "art_") || seen[ref.value] {
			return invalid(report, ref.path, "Select a distinct verified artifact with the required contract.")
		}
		seen[ref.value] = true
	}
	normalize(&spec)
	if issue := validateRequest(spec); issue != nil {
		return invalid(report, issue.path, issue.message)
	}
	if spec.Action == "write" || spec.Action == "delete" {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Message: "The selected file will be changed inside the managed export. Review the path and plan hash before starting."})
	} else {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The request is read-only and remains confined to the verified managed export."})
	}
	if !spec.ProtectOutput {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Message: "The storage result will be downloadable and previewable from the browser."})
	}
	return report
}

func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	normalize(&spec)
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "storage-" + spec.Action, Name: actionTitle(spec.Action), Input: input, Mutating: spec.Action == "write" || spec.Action == "delete",
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
			{Name: "storage-endpoint", Type: "SharedStorageEndpoint", Version: "v1alpha1", ArtifactID: spec.SharedStorageEndpointRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "storage-view", Type: "SharedStorageView", Version: "v1alpha1", MediaType: "application/json", Source: "/storageView", Sensitive: spec.ProtectOutput}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, endpoint, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(machines, access, endpoint); err != nil {
		return unhealthy(err.Error(), "storage", "invalid"), nil
	}
	if issue := validateRequest(spec); issue != nil {
		return unhealthy(issue.message, "request", "invalid"), nil
	}
	if err := log("info", "Validating managed export ownership, SSH access and confined target path"); err != nil {
		return domain.HealthReport{}, err
	}
	command := "sudo test -s " + managedMarker + " && sudo test -d " + managedExport + " && command -v realpath >/dev/null && command -v base64 >/dev/null && command -v sha256sum >/dev/null && command -v head >/dev/null && { " + targetScript(spec.Path) + "; }"
	if _, err := plugin.runner().Run(ctx, machines.Spec.Machines[0], access, command); err != nil {
		return unhealthy("Managed storage preflight failed: "+err.Error(), "ssh", "unreachable"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed NFS export is reachable and the target path is confined", Checks: map[string]string{"ssh": "verified", "export": "owned", "path": "confined", "action": spec.Action}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, machines, access, endpoint, err := resolve(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", actionTitle(spec.Action)+" at "+spec.Path); err != nil {
		return nil, err
	}
	view := storageViewSpec{Action: spec.Action, Server: endpoint.Spec.Server, ExportPath: endpoint.Spec.ExportPath, Path: spec.Path}
	runner := plugin.runner()
	target := machines.Spec.Machines[0]
	switch spec.Action {
	case "list":
		output, err := runner.Run(ctx, target, access, targetScript(spec.Path)+"; sudo test -d \"$target\" && sudo find \"$target\" -mindepth 1 -maxdepth 1 -printf '%f\\0%y\\0%s\\0%T@\\0' | head -z -n "+strconv.Itoa(listingFieldLimit))
		if err != nil {
			return nil, err
		}
		entries, err := parseListing(output)
		if err != nil {
			return nil, err
		}
		view.State, view.Entries, view.Truncated = "listed", entries, len(entries) == listingFieldLimit/4
	case "read":
		output, err := runner.Run(ctx, target, access, targetScript(spec.Path)+fmt.Sprintf("; test ! -L \"$requested\" && sudo test -f \"$target\" && test \"$(sudo stat -c %%s -- \"$target\")\" -le %d && sudo base64 -w0 -- \"$target\"", readSizeLimit))
		if err != nil {
			return nil, err
		}
		content, err := base64.StdEncoding.DecodeString(strings.TrimSpace(output))
		if err != nil || len(content) > readSizeLimit {
			return nil, errors.New("managed file content is invalid or exceeds 512 KiB")
		}
		view.State, view.ContentBase64, view.SizeBytes, view.Digest = "read", base64.StdEncoding.EncodeToString(content), int64(len(content)), digest(content)
	case "write":
		content := []byte(spec.Content)
		encoded := base64.StdEncoding.EncodeToString(content)
		command := targetScript(spec.Path) + "; test ! -L \"$requested\"; temporary=$(mktemp " + managedExport + "/.kubephos-upload.XXXXXX) || exit 125; trap 'rm -f \"$temporary\"' EXIT HUP INT TERM; printf '%s' " + shellQuote(encoded) + " | base64 -d >\"$temporary\"; chmod 0666 \"$temporary\"; sudo install -d -m 0777 \"$(dirname -- \"$target\")\"; sudo mv -- \"$temporary\" \"$target\"; trap - EXIT HUP INT TERM; sudo sha256sum -- \"$target\" | cut -d' ' -f1"
		output, err := runner.Run(ctx, target, access, command)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(output) != strings.TrimPrefix(digest(content), "sha256:") {
			return nil, errors.New("managed file digest does not match uploaded content")
		}
		view.State, view.SizeBytes, view.Digest = "written", int64(len(content)), digest(content)
	case "delete":
		if _, err := runner.Run(ctx, target, access, targetScript(spec.Path)+"; test ! -L \"$requested\" && sudo test -f \"$target\" && sudo rm -- \"$target\""); err != nil {
			return nil, err
		}
		view.State = "deleted"
	}
	return marshalView(spec.Path, view)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, endpoint, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	view, err := unmarshalView(raw)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if view.Action != spec.Action || view.Server != endpoint.Spec.Server || view.ExportPath != endpoint.Spec.ExportPath || view.Path != spec.Path {
		return unhealthy("Storage view artifact does not match the validated request", "artifact", "invalid"), nil
	}
	command := "sudo test -s " + managedMarker + " && sudo test -d " + managedExport
	switch spec.Action {
	case "read", "write":
		if view.Digest == "" {
			return unhealthy("Storage result digest is missing", "artifact", "invalid"), nil
		}
		command += " && { " + targetScript(spec.Path) + "; test ! -L \"$requested\" && sudo test -f \"$target\" && test \"$(sudo sha256sum -- \"$target\" | cut -d' ' -f1)\" = " + shellQuote(strings.TrimPrefix(view.Digest, "sha256:")) + "; }"
	case "delete":
		command += " && { " + targetScript(spec.Path) + "; test ! -e \"$target\" && test ! -L \"$requested\"; }"
	}
	if _, err := plugin.runner().Run(ctx, machines.Spec.Machines[0], access, command); err != nil {
		return unhealthy("Managed storage post-condition failed: "+err.Error(), "storage", "unhealthy"), nil
	}
	if err := log("info", "Storage result, export ownership and requested post-condition are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed storage action completed and the export remains healthy", Checks: map[string]string{"ssh": "healthy", "export": "owned", "path": "confined", "action": "verified"}}, nil
}

func (Plugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

type validationError struct {
	path    string
	message string
}

func validateRequest(spec Spec) *validationError {
	if spec.Action != "list" && spec.Action != "read" && spec.Action != "write" && spec.Action != "delete" {
		return &validationError{"action", "Select a supported storage action."}
	}
	if !validRelativePath(spec.Path) {
		return &validationError{"path", "Path must remain relative to the managed export."}
	}
	if spec.Action != "list" && spec.Path == "." {
		return &validationError{"path", "Select a file inside the managed export."}
	}
	if len([]byte(spec.Content)) > writeSizeLimit || !utf8.ValidString(spec.Content) || invalidText(spec.Content) {
		return &validationError{"content", "Content must be valid text no larger than 128 KiB."}
	}
	return nil
}

func validRelativePath(value string) bool {
	if value == "." {
		return true
	}
	if value == "" || len(value) > 512 || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || path.Clean(value) != value || value == ".." || strings.HasPrefix(value, "../") {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." || invalidText(segment) {
			return false
		}
	}
	return true
}

func invalidText(value string) bool {
	for _, current := range value {
		if current == 0 || unicode.IsControl(current) && current != '\n' && current != '\r' && current != '\t' {
			return true
		}
	}
	return false
}

func normalize(spec *Spec) {
	spec.Action = strings.TrimSpace(spec.Action)
	spec.Path = strings.TrimSpace(spec.Path)
}

func resolve(step domain.PlanStep) (Spec, machineSet, machineAccess, sharedStorageEndpoint, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, sharedStorageEndpoint{}, err
	}
	normalize(&spec)
	machineValue, machineOK := step.ResolvedInputs["machines"]
	accessValue, accessOK := step.ResolvedInputs["machine-access"]
	endpointValue, endpointOK := step.ResolvedInputs["storage-endpoint"]
	if !machineOK || !accessOK || !endpointOK {
		return Spec{}, machineSet{}, machineAccess{}, sharedStorageEndpoint{}, errors.New("verified managed storage inputs are unavailable")
	}
	var machines machineSet
	var access machineAccess
	var endpoint sharedStorageEndpoint
	if err := json.Unmarshal(machineValue.Value, &machines); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, sharedStorageEndpoint{}, err
	}
	if err := json.Unmarshal(accessValue.Value, &access); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, sharedStorageEndpoint{}, err
	}
	if err := json.Unmarshal(endpointValue.Value, &endpoint); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, sharedStorageEndpoint{}, err
	}
	return spec, machines, access, endpoint, nil
}

func validateArtifacts(machines machineSet, access machineAccess, endpoint sharedStorageEndpoint) error {
	if machines.APIVersion != artifactAPI || machines.Kind != "MachineSet" || len(machines.Spec.Machines) != 1 {
		return errors.New("managed NFS storage requires one dedicated machine")
	}
	_, network, err := net.ParseCIDR(machines.Spec.NetworkCIDR)
	if err != nil || network.String() != machines.Spec.NetworkCIDR {
		return errors.New("managed machine network is invalid")
	}
	target := machines.Spec.Machines[0]
	address := net.ParseIP(target.Address)
	if target.ID == "" || target.Name == "" || target.SSHPort != 22 || target.SSHUser == "" || target.State != "running" || address == nil || address.To4() == nil || !network.Contains(address) {
		return errors.New("managed storage machine is invalid")
	}
	if access.APIVersion != artifactAPI || access.Kind != "MachineAccess" || access.Spec.Algorithm != "ssh-ed25519" {
		return errors.New("machine access identity is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey))
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != strings.TrimSpace(access.Spec.PublicKey) {
		return errors.New("machine access key pair is invalid")
	}
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "SharedStorageEndpoint" || endpoint.Spec.Protocol != "nfs" || endpoint.Spec.Server != target.Address || endpoint.Spec.ExportPath != managedExport || endpoint.Spec.ClientCIDR != machines.Spec.NetworkCIDR {
		return errors.New("managed storage endpoint does not match the dedicated machine")
	}
	return nil
}

func targetScript(relative string) string {
	requested := managedExport
	if relative != "." {
		requested += "/" + relative
	}
	return "base=" + shellQuote(managedExport) + "; requested=" + shellQuote(requested) + "; target=$(realpath -m -- \"$requested\") || exit 64; case \"$target\" in \"$base\"|\"$base\"/*) true;; *) exit 64;; esac"
}

func parseListing(value string) ([]fileEntry, error) {
	fields := strings.Split(value, string(rune(0)))
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields)%4 != 0 || len(fields) > listingFieldLimit {
		return nil, errors.New("managed directory listing is invalid")
	}
	entries := make([]fileEntry, 0, len(fields)/4)
	for position := 0; position < len(fields); position += 4 {
		size, sizeErr := strconv.ParseInt(fields[position+2], 10, 64)
		modified, modifiedErr := strconv.ParseFloat(fields[position+3], 64)
		if fields[position] == "" || sizeErr != nil || modifiedErr != nil {
			return nil, errors.New("managed directory entry is invalid")
		}
		entries = append(entries, fileEntry{Name: fields[position], Type: fileType(fields[position+1]), SizeBytes: size, ModifiedUnix: modified})
	}
	sort.Slice(entries, func(left, right int) bool { return entries[left].Name < entries[right].Name })
	return entries, nil
}

func marshalView(name string, spec storageViewSpec) (json.RawMessage, error) {
	return json.Marshal(map[string]any{"storageView": map[string]any{
		"apiVersion": artifactAPI, "kind": "SharedStorageView",
		"metadata": map[string]any{"name": name, "version": "v1alpha1", "createdAt": time.Now().UTC()},
		"spec":     spec,
	}})
}

func unmarshalView(raw json.RawMessage) (storageViewSpec, error) {
	var value struct {
		StorageView struct {
			APIVersion string
			Kind       string
			Metadata   struct{ Version string }
			Spec       storageViewSpec
		}
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return storageViewSpec{}, err
	}
	if value.StorageView.APIVersion != artifactAPI || value.StorageView.Kind != "SharedStorageView" || value.StorageView.Metadata.Version != "v1alpha1" {
		return storageViewSpec{}, errors.New("storage view identity is invalid")
	}
	return value.StorageView.Spec, nil
}

func fileType(value string) string {
	result := map[string]string{"f": "file", "d": "directory", "l": "link", "b": "block", "c": "character", "p": "pipe", "s": "socket"}[value]
	if result == "" {
		return "unknown"
	}
	return result
}

func digest(value []byte) string {
	current := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(current[:])
}

func actionTitle(action string) string {
	return map[string]string{"list": "List managed NFS path", "read": "Read managed NFS file", "write": "Write managed NFS file", "delete": "Delete managed NFS file"}[action]
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return &sshRunner{client: pluginssh.New()}
}

type sshRunner struct {
	client *pluginssh.Client
}

func (runner *sshRunner) Run(ctx context.Context, target machine, access machineAccess, command string) (string, error) {
	output, err := runner.client.Run(ctx, pluginssh.Target{Address: target.Address, Port: target.SSHPort, User: target.SSHUser}, access.Spec.PrivateKey, command)
	if err != nil {
		return "", errors.New("managed storage command failed")
	}
	return output, nil
}
