package kubeconfig

import (
	"strings"
	"testing"
)

func TestValidateClientIdentity(t *testing.T) {
	valid := `apiVersion: v1
kind: Config
current-context: selected
contexts:
- name: selected
  context: {cluster: cluster, user: admin}
clusters:
- name: cluster
  cluster: {server: 'https://cluster.test:6443'}
users:
- name: admin
  user: {client-certificate-data: Y2VydA==, client-key-data: a2V5}
`
	tests := []struct {
		name, value, server string
		valid               bool
	}{
		{"valid", valid, "https://cluster.test:6443", true},
		{"wrong server", valid, "https://other.test:6443", false},
		{"missing context", strings.ReplaceAll(valid, "current-context: selected", "current-context: missing"), "https://cluster.test:6443", false},
		{"missing user", strings.ReplaceAll(valid, "user: admin", "user: missing"), "https://cluster.test:6443", false},
		{"invalid key", strings.ReplaceAll(valid, "a2V5", "%%%"), "https://cluster.test:6443", false},
		{"empty certificate", strings.ReplaceAll(valid, "Y2VydA==", "''"), "https://cluster.test:6443", false},
		{"invalid yaml", "[invalid", "https://cluster.test:6443", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateClientIdentity(test.value, test.server)
			if (err == nil) != test.valid {
				t.Fatalf("unexpected validation: %v", err)
			}
		})
	}
}
