package plugins

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
)

func TestPackagedManifestsLoad(t *testing.T) {
	paths, err := filepath.Glob("../../plugins/*/plugin.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("manifest discovery: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var definition descriptor
			if err := yaml.Unmarshal(raw, &definition); err != nil {
				t.Fatal(err)
			}
			directory := t.TempDir()
			if err := os.WriteFile(filepath.Join(directory, definition.Spec.Runtime.Executable), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
				t.Fatal(err)
			}
			if _, err := loadDefinition(raw, directory, nil, nil, nil, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}
