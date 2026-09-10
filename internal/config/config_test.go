package config

import (
	"reflect"
	"testing"
)

func TestLoadPluginRegistryAllowlist(t *testing.T) {
	t.Setenv("KUBEPHOS_PLUGIN_ALLOWED_REGISTRIES", " harbor.example.test:5443, registry.example.test ,,harbor.example.test:5443 ")
	value := Load()
	expected := []string{"harbor.example.test:5443", "registry.example.test", "harbor.example.test:5443"}
	if !reflect.DeepEqual(value.PluginRegistries, expected) {
		t.Fatalf("unexpected registries: %#v", value.PluginRegistries)
	}
}
