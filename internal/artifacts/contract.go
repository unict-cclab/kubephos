package artifacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

const MaxArtifactBytes = 16 * 1024 * 1024

var artifactIdentity = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9.-]{0,127}$`)
var artifactVersion = regexp.MustCompile(`^v[0-9]+(?:alpha[0-9]+|beta[0-9]+)?$`)
var outputName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

func ValidatePlan(plan domain.Plan, declaredInputs, declaredOutputs []domain.ArtifactContract) error {
	allowedInputs, err := validateContracts(declaredInputs)
	if err != nil {
		return fmt.Errorf("plugin artifact inputs: %w", err)
	}
	allowedOutputs, err := validateContracts(declaredOutputs)
	if err != nil {
		return fmt.Errorf("plugin artifact outputs: %w", err)
	}
	producers := map[string]domain.ArtifactOutput{}
	for _, step := range plan.Steps {
		inputs := map[string]bool{}
		for _, input := range step.ArtifactInputs {
			if !outputName.MatchString(input.Name) || inputs[input.Name] {
				return fmt.Errorf("step %q contains an invalid or duplicate artifact input name", step.ID)
			}
			inputs[input.Name] = true
			if err := validateIdentity(input.Type, input.Version); err != nil {
				return fmt.Errorf("step %q input %q: %w", step.ID, input.Name, err)
			}
			if !allowedInputs[input.Type+"\x00"+input.Version] {
				return fmt.Errorf("step %q input %q uses undeclared artifact contract %s/%s", step.ID, input.Name, input.Type, input.Version)
			}
			if input.ArtifactID != "" {
				if !strings.HasPrefix(input.ArtifactID, "art_") || input.FromStep != "" || input.FromOutput != "" {
					return fmt.Errorf("step %q input %q contains an invalid external artifact reference", step.ID, input.Name)
				}
				continue
			}
			if input.FromStep == "" || input.FromOutput == "" {
				return fmt.Errorf("step %q input %q does not declare an artifact source", step.ID, input.Name)
			}
			producer, exists := producers[input.FromStep+"\x00"+input.FromOutput]
			if !exists {
				return fmt.Errorf("step %q input %q references an unavailable earlier output", step.ID, input.Name)
			}
			if producer.Type != input.Type || producer.Version != input.Version {
				return fmt.Errorf("step %q input %q requires %s/%s but its source provides %s/%s", step.ID, input.Name, input.Type, input.Version, producer.Type, producer.Version)
			}
		}
		outputs := map[string]bool{}
		for _, output := range step.Outputs {
			if !outputName.MatchString(output.Name) || outputs[output.Name] {
				return fmt.Errorf("step %q contains an invalid or duplicate artifact output name", step.ID)
			}
			outputs[output.Name] = true
			if err := validateIdentity(output.Type, output.Version); err != nil {
				return fmt.Errorf("step %q output %q: %w", step.ID, output.Name, err)
			}
			if !allowedOutputs[output.Type+"\x00"+output.Version] {
				return fmt.Errorf("step %q output %q uses undeclared artifact contract %s/%s", step.ID, output.Name, output.Type, output.Version)
			}
			if output.MediaType != "application/json" {
				return fmt.Errorf("step %q output %q uses unsupported media type %q", step.ID, output.Name, output.MediaType)
			}
			if err := validatePointer(output.Source); err != nil {
				return fmt.Errorf("step %q output %q: %w", step.ID, output.Name, err)
			}
			producers[step.ID+"\x00"+output.Name] = output
		}
	}
	return nil
}

func Digest(value []byte) string {
	digest := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func Verify(value []byte, expectedDigest string, expectedSize int64) error {
	if int64(len(value)) != expectedSize {
		return fmt.Errorf("artifact size mismatch: expected %d, received %d", expectedSize, len(value))
	}
	actual := Digest(value)
	if actual != expectedDigest {
		return fmt.Errorf("artifact digest mismatch: expected %s, received %s", expectedDigest, actual)
	}
	return nil
}

func validateContracts(contracts []domain.ArtifactContract) (map[string]bool, error) {
	result := make(map[string]bool, len(contracts))
	for _, contract := range contracts {
		if err := validateIdentity(contract.Type, contract.Version); err != nil {
			return nil, err
		}
		key := contract.Type + "\x00" + contract.Version
		if result[key] {
			return nil, fmt.Errorf("duplicate contract %s/%s", contract.Type, contract.Version)
		}
		result[key] = true
	}
	return result, nil
}

func ExtractJSON(raw json.RawMessage, pointer string) (json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode step result: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("step result contains trailing JSON")
	}
	if pointer != "" {
		for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
			token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
			switch current := value.(type) {
			case map[string]any:
				var exists bool
				value, exists = current[token]
				if !exists {
					return nil, fmt.Errorf("artifact source %q is missing", pointer)
				}
			case []any:
				index, err := strconv.ParseUint(token, 10, 31)
				if err != nil || int(index) >= len(current) {
					return nil, fmt.Errorf("artifact source %q contains an invalid array index", pointer)
				}
				value = current[index]
			default:
				return nil, fmt.Errorf("artifact source %q does not resolve to an object field", pointer)
			}
		}
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if len(result) > MaxArtifactBytes {
		return nil, fmt.Errorf("artifact exceeds %d bytes", MaxArtifactBytes)
	}
	return result, nil
}

func validateIdentity(artifactType, version string) error {
	if !artifactIdentity.MatchString(artifactType) {
		return errors.New("artifact type is invalid")
	}
	if !artifactVersion.MatchString(version) {
		return errors.New("artifact version is invalid")
	}
	return nil
}

func validatePointer(pointer string) error {
	if pointer == "" {
		return nil
	}
	if !strings.HasPrefix(pointer, "/") {
		return errors.New("artifact source must be an RFC 6901 JSON pointer")
	}
	for position := 0; position < len(pointer); position++ {
		if pointer[position] != '~' {
			continue
		}
		if position+1 >= len(pointer) || (pointer[position+1] != '0' && pointer[position+1] != '1') {
			return errors.New("artifact source contains an invalid JSON pointer escape")
		}
		position++
	}
	return nil
}
