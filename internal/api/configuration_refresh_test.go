package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

func TestConfigurationRefreshKeepsRevisionForJSONBFormatting(t *testing.T) {
	for _, definition := range []string{
		`{"kind":"suite","runs":1,"variants":[{"configurationId":"a","color":"#123456"},{"configurationId":"b","color":"#654321"}]}`,
		`{"kind":"single","applicationValues":{"services":15},"components":[]}`,
	} {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(definition), &decoded); err != nil {
			t.Fatal(err)
		}
		stored, err := json.MarshalIndent(decoded, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		source := domain.ExperimentConfiguration{ID: "saved", WorkspaceID: "workspace", ClusterResourceID: "cluster", ApplicationDigest: "digest", Definition: stored, UpdatedAt: time.Unix(123, 0), Validation: domain.ValidationReport{Valid: true}}
		updated := source
		updated.Definition = json.RawMessage(definition)
		// No store is needed: unchanged content must not attempt a database write.
		result, failure := (&Server{}).persistRevalidatedExperimentConfiguration(context.Background(), source, updated)
		if failure != nil || !result.UpdatedAt.Equal(source.UpdatedAt) || string(result.Definition) != string(stored) {
			t.Fatalf("formatting changed the saved revision: %#v, %v", result, failure)
		}
	}
}

func TestConfigurationJSONDetectsRealChanges(t *testing.T) {
	for _, pair := range [][2]string{
		{`{"color":"#123456"}`, `{"color":"#654321"}`},
		{`{"runs":1}`, `{"runs":2}`},
		{`{"members":["a","b"]}`, `{"members":["b","a"]}`},
		{`{"runs":1}`, `{"runs":"1"}`},
		{`{"value":9007199254740992}`, `{"value":9007199254740993}`},
	} {
		if sameConfigurationJSON(json.RawMessage(pair[0]), json.RawMessage(pair[1])) {
			t.Fatalf("real change ignored: %v", pair)
		}
	}
}

func TestCatalogRefreshPreservesConfigurationOwnership(t *testing.T) {
	source := domain.ExperimentConfiguration{ID: "saved", OwnerUserID: "user", OwnerUsername: "owner", CreatedAt: time.Unix(123, 0), ApplicationDigest: "old", Definition: json.RawMessage(`{"replicas":3}`)}
	revalidated := domain.ExperimentConfiguration{ID: "generated", ApplicationDigest: "new", Definition: json.RawMessage(`{"replicas":3,"telemetry":false}`), Validation: domain.ValidationReport{Valid: true}}
	updated := retainConfigurationIdentity(source, revalidated)
	if updated.ID != source.ID || updated.OwnerUserID != source.OwnerUserID || updated.OwnerUsername != source.OwnerUsername || !updated.CreatedAt.Equal(source.CreatedAt) {
		t.Fatalf("catalog refresh changed the identity or owner: %#v", updated)
	}
	if updated.ApplicationDigest != "new" || string(updated.Definition) != string(revalidated.Definition) || !updated.Validation.Valid {
		t.Fatalf("catalog refresh lost the validated package: %#v", updated)
	}
	if source.ApplicationDigest != "old" || string(source.Definition) != `{"replicas":3}` {
		t.Fatal("catalog refresh mutated the source snapshot")
	}
}

func TestConfigurationRefreshDetectsPluginUpgradeWithoutApplicationChange(t *testing.T) {
	server := &Server{registry: plugins.NewRegistry(manifestOnlyPlugin{plugins.Manifest{ID: "descheduler", Version: "0.2.1"}})}
	definition := experimentConfigurationDefinitionInput{Components: []experimentConfigurationComponentInput{{PluginID: "descheduler", PluginVersion: "0.2.0"}}}
	if !server.configurationPluginsChanged(definition) {
		t.Fatal("plugin upgrade must refresh the configuration even if the application is unchanged")
	}
	definition.Components[0].PluginVersion = "0.2.1"
	if server.configurationPluginsChanged(definition) {
		t.Fatal("current plugin must not require refresh")
	}
	definition.Components[0].PluginID = "removed"
	if !server.configurationPluginsChanged(definition) {
		t.Fatal("removed plugin must trigger revalidation instead of leaving the configuration valid")
	}
}
