package kubeconfig

import (
	"strings"
	"testing"
)

func TestWithIdentityRenamesCurrentEntriesAndPreservesCredentials(t *testing.T) {
	input := `apiVersion: v1
kind: Config
clusters:
- name: default
  cluster:
    certificate-authority-data: Y2E=
    server: https://10.0.0.10:6443
users:
- name: default
  user:
    client-certificate-data: Y2VydA==
    client-key-data: a2V5
contexts:
- name: default
  context:
    cluster: default
    namespace: development
    user: default
current-context: default
`
	output, err := WithIdentity(input, "paper-cluster")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"name: paper-cluster", "name: paper-cluster-admin", "cluster: paper-cluster", "user: paper-cluster-admin", "current-context: paper-cluster", "namespace: development", "client-key-data: a2V5"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected %q in kubeconfig:\n%s", expected, output)
		}
	}
}

func TestWithIdentityRejectsMissingCurrentReferences(t *testing.T) {
	_, err := WithIdentity("apiVersion: v1\nkind: Config\ncurrent-context: missing\ncontexts: []\nclusters: []\nusers: []\n", "dev")
	if err == nil {
		t.Fatal("expected invalid kubeconfig rejection")
	}
}
