package api

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestComparativeConfigurationValidation(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		change     func(*experimentConfigurationInput, map[string]domain.ExperimentConfiguration)
	}{
		{name: "valid"},
		{name: "one member", want: "invalid_suite", change: func(i *experimentConfigurationInput, _ map[string]domain.ExperimentConfiguration) {
			i.Definition.Variants = i.Definition.Variants[:1]
		}},
		{name: "duplicate", want: "invalid_suite", change: func(i *experimentConfigurationInput, _ map[string]domain.ExperimentConfiguration) {
			i.Definition.Variants[1].ConfigurationID = "a"
		}},
		{name: "missing", want: "invalid_configuration", change: func(_ *experimentConfigurationInput, m map[string]domain.ExperimentConfiguration) { delete(m, "b") }},
		{name: "nested", want: "nested_suite", change: func(_ *experimentConfigurationInput, m map[string]domain.ExperimentConfiguration) {
			c := m["b"]
			c.Kind = "suite"
			m["b"] = c
		}},
		{name: "different cluster", want: "incompatible_suite", change: func(_ *experimentConfigurationInput, m map[string]domain.ExperimentConfiguration) {
			c := m["b"]
			c.ClusterResourceID = "other"
			m["b"] = c
		}},
		{name: "different destination", want: "incompatible_suite", change: func(_ *experimentConfigurationInput, m map[string]domain.ExperimentConfiguration) {
			c := m["b"]
			c.ApplicationRef = "other"
			m["b"] = c
		}},
		{name: "invalid member", want: "invalid_configuration", change: func(_ *experimentConfigurationInput, m map[string]domain.ExperimentConfiguration) {
			c := m["b"]
			c.Validation.Valid = false
			m["b"] = c
		}},
		{name: "duplicate aliases", want: "invalid_suite_presentation", change: func(i *experimentConfigurationInput, _ map[string]domain.ExperimentConfiguration) {
			i.Definition.Variants[0].Alias = "Test"
			i.Definition.Variants[1].Alias = "test"
		}},
		{name: "duplicate colors", want: "invalid_suite_presentation", change: func(i *experimentConfigurationInput, _ map[string]domain.ExperimentConfiguration) {
			i.Definition.Variants[0].Color = "#123456"
			i.Definition.Variants[1].Color = "#123456"
		}},
		{name: "invalid runs", want: "invalid_suite", change: func(i *experimentConfigurationInput, _ map[string]domain.ExperimentConfiguration) {
			i.Definition.Runs = 21
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := map[string]domain.ExperimentConfiguration{}
			for _, id := range []string{"a", "b"} {
				members[id] = domain.ExperimentConfiguration{ID: id, Kind: "single", Name: id, WorkspaceID: "workspace", ClusterResourceID: "cluster", ApplicationRef: "app:test", Validation: domain.ValidationReport{Valid: true}}
			}
			input := experimentConfigurationInput{Kind: "suite", Name: "Comparison", Definition: experimentConfigurationDefinitionInput{Variants: []experimentSuiteVariantInput{{ConfigurationID: "a"}, {ConfigurationID: "b"}}}}
			if tc.change != nil {
				tc.change(&input, members)
			}
			configuration, failure := validateComparativeConfiguration(input, func(id string) (domain.ExperimentConfiguration, error) {
				member, ok := members[id]
				if !ok {
					return member, errUnavailableConfiguration
				}
				return member, nil
			})
			if tc.want != "" {
				if failure == nil || failure.code != tc.want {
					t.Fatalf("want %q, got %#v", tc.want, failure)
				}
				return
			}
			if failure != nil || configuration.Kind != "suite" || configuration.ClusterResourceID != "cluster" {
				t.Fatalf("unexpected configuration: %#v, %v", configuration, failure)
			}
			var definition experimentConfigurationDefinitionInput
			if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
				t.Fatal(err)
			}
			if definition.Runs != 3 || len(definition.Variants) != 2 || definition.Variants[0].Alias != "a" || definition.Variants[0].Color == definition.Variants[1].Color {
				t.Fatalf("unexpected recipe %#v", definition)
			}
		})
	}
}

func TestExecutionSnapshotFreezesSuiteAndSingleSettings(t *testing.T) {
	configuration := domain.ExperimentConfiguration{ID: "suite", Kind: "suite", Definition: json.RawMessage(`{"kind":"suite","runs":4}`)}
	member := domain.ExperimentConfiguration{ID: "single", ClusterResourceID: "cluster", ApplicationRef: "app:test", ApplicationDigest: "original", Definition: json.RawMessage(`{"components":[{"configuration":{"rps":10}}]}`)}
	snapshot := executionConfigurationSnapshot(configuration, []validatedSuiteVariant{{Configuration: member}}, 4)
	configuration.Definition[0] = ' '
	member.Definition[0] = ' '
	member.ApplicationDigest = "changed"
	var saved struct {
		Configuration domain.ExperimentConfiguration
		Members       []domain.ExperimentConfiguration
		Runs          int
	}
	if err := json.Unmarshal(snapshot, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Runs != 4 || string(saved.Configuration.Definition) != `{"kind":"suite","runs":4}` || saved.Members[0].ApplicationDigest != "original" || saved.Members[0].ClusterResourceID != "cluster" || string(saved.Members[0].Definition) != `{"components":[{"configuration":{"rps":10}}]}` {
		t.Fatalf("snapshot changed: %s", snapshot)
	}
}
