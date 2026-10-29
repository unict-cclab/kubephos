package mesh

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func testHelmRelease(values map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"version": 6, "config": values})
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	_, _ = writer.Write(raw)
	_ = writer.Close()
	return base64.StdEncoding.EncodeToString([]byte(base64.StdEncoding.EncodeToString(buffer.Bytes())))
}
func testHelmReleaseList(values map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"items": []any{map[string]any{"data": map[string]string{"release": testHelmRelease(values)}}}})
	return string(raw)
}

type persistenceRunner struct {
	meshTestRunner
	manifests []map[string]any
	patch     map[string]any
	failWrite bool
	foreign   bool
}

func (r *persistenceRunner) Kubectl(_ context.Context, _ string, input []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if strings.HasPrefix(command, "get helmchartconfig ") {
		return `{"spec":{"valuesContent":"operator: {}","valuesSecrets":[{"name":"user-values","keys":["user.yaml"]}]}}`, nil
	}
	if strings.HasPrefix(command, "get secret "+meshValuesSecret) {
		if r.foreign {
			return `{"metadata":{"labels":{"app.kubernetes.io/managed-by":"another-tool"}}}`, nil
		}
		return "", nil
	}
	if len(input) > 0 {
		if r.failWrite {
			return "private-key-value", errors.New("private-key-value")
		}
		var m map[string]any
		_ = json.Unmarshal(input, &m)
		r.manifests = append(r.manifests, m)
	}
	if args[0] == "patch" {
		_ = json.Unmarshal([]byte(args[len(args)-1]), &r.patch)
	}
	return "applied", nil
}
func TestMeshValuesRemainDeclaredWithoutExposingSecrets(t *testing.T) {
	values := map[string]any{"clustermesh": map[string]any{"config": map[string]any{"enabled": true, "clusters": map[string]any{"peer": map[string]any{"ips": []string{"192.0.2.1"}, "port": 32379, "tls": map[string]string{"key": "private-key-value"}}}}}, "tls": map[string]any{"caBundle": map[string]any{"enabled": true, "content": "public-ca-bundle"}}, "unrelatedSetting": "not-owned"}
	decoded, version, err := HelmReleaseValues(testHelmRelease(values))
	if err != nil || version != 6 {
		t.Fatal("release decode failed", err)
	}
	r := &persistenceRunner{}
	if err = PersistMeshConfiguration(context.Background(), r, "config", "environment", decoded, []string{"peer"}); err != nil {
		t.Fatal(err)
	}
	if len(r.manifests) != 1 || r.manifests[0]["kind"] != "Secret" {
		t.Fatal("sensitive values must stay in a Secret")
	}
	data := r.manifests[0]["data"].(map[string]any)
	raw, _ := base64.StdEncoding.DecodeString(data["values.yaml"].(string))
	var persisted map[string]any
	if yaml.Unmarshal(raw, &persisted) != nil {
		t.Fatal("invalid persisted YAML")
	}
	if persisted["unrelatedSetting"] != nil || persisted["tls"] == nil || !strings.Contains(string(raw), "private-key-value") {
		t.Fatal("did not preserve only mesh and CA settings")
	}
	patch, _ := json.Marshal(r.patch)
	if strings.Contains(string(patch), "private-key-value") || !strings.Contains(string(patch), "user-values") || !strings.Contains(string(patch), meshValuesSecret) {
		t.Fatal("lost user values or exposed secrets in HelmChartConfig")
	}
}
func TestMeshPersistenceRejectsMissingMembershipForeignOwnershipAndSecretErrorLeaks(t *testing.T) {
	values := map[string]any{"clustermesh": map[string]any{"config": map[string]any{"enabled": true, "clusters": map[string]any{}}}}
	if _, err := meshValues(values, []string{"peer"}); err == nil {
		t.Fatal("accepted an empty mesh")
	}
	for _, r := range []*persistenceRunner{{foreign: true}, {failWrite: true}} {
		err := PersistMeshConfiguration(context.Background(), r, "config", "environment", values, nil)
		if err == nil || strings.Contains(err.Error(), "private-key-value") {
			t.Fatalf("unsafe persistence failure: %v", err)
		}
	}
}
func TestHealthyButEmptyMeshFailsExpectedMembershipCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := waitForMeshMembership(ctx, &persistenceRunner{}, "config", []string{"missing-peer"})
	if err == nil || !strings.Contains(err.Error(), "expected 1") {
		t.Fatal("0/0 mesh was accepted")
	}
}

func TestMeshDisconnectPersistsEmptyMembership(t *testing.T) {
	runner := &meshTestRunner{}
	if err := (Plugin{Runner: runner}).Cleanup(context.Background(), fixtureStep(t), nil, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(runner.managedSecrets) != 3 {
		t.Fatal("disconnect did not persist each member")
	}
	for _, raw := range runner.managedSecrets {
		var secret struct{ Data map[string]string }
		_ = json.Unmarshal(raw, &secret)
		data, err := base64.StdEncoding.DecodeString(secret.Data["values.yaml"])
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]any
		if yaml.Unmarshal(data, &values) != nil {
			t.Fatal("invalid persisted disconnect values")
		}
		if _, err = meshValues(values, nil); err != nil {
			t.Fatal("reconciliation would reconnect old peers", err)
		}
	}
}

func TestMeshValuesAcceptLegacyListAndRejectDisabledMembership(t *testing.T) {
	values := map[string]any{"clustermesh": map[string]any{"config": map[string]any{"enabled": true, "clusters": []any{map[string]any{"name": "peer"}}}}}
	if _, err := meshValues(values, []string{"peer"}); err != nil {
		t.Fatal(err)
	}
	values["clustermesh"].(map[string]any)["config"].(map[string]any)["enabled"] = false
	if _, err := meshValues(values, []string{"peer"}); err == nil {
		t.Fatal("accepted disabled connections")
	}
}
