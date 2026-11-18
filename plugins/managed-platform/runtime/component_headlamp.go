package managedplatform

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
)

// Headlamp is the cluster-admin Kubernetes console; its token becomes a protected credential.
const (
	headlampVersion     = "0.45.0"
	headlampRelease     = "kubephos-headlamp"
	headlampPort        = 30003
	headlampTokenSecret = "kubephos-headlamp-token"
)

var headlampComponent = platformComponent{
	enabled:  func(s selection) bool { return s.has(managedcomponents.Headlamp) },
	charts:   []chartSpec{{ID: "headlamp", URL: "https://github.com/kubernetes-sigs/headlamp/releases/download/headlamp-helm-0.45.0/headlamp-0.45.0.tgz", Digest: "38c619e0ed5db4164b02c353bcbb24e8157c1d98d961b292f99d68cc9316b8eb"}},
	releases: []releaseRef{{headlampRelease, observabilityNamespace}},
	install: func(ctx context.Context, run *componentRun) error {
		if err := run.log("info", "Installing the managed cluster-admin Headlamp dashboard"); err != nil {
			return err
		}
		if err := run.helm(ctx, "headlamp", headlampValues(run.marker), headlampRelease, observabilityNamespace, "10m"); err != nil {
			return fmt.Errorf("install Headlamp: %w", err)
		}
		token, err := readHeadlampToken(ctx, run.runner, run.kubeconfig)
		if err != nil {
			return err
		}
		run.result.HeadlampCredential = &serviceCredential{APIVersion: artifactAPI, Kind: "ServiceCredential", Metadata: serviceCredentialMetadata{Name: "headlamp-admin", Role: "cluster-admin"}, Spec: serviceCredentialSpec{Namespace: observabilityNamespace, Service: headlampRelease, Username: "token", Password: token}}
		return nil
	},
	verify: func(ctx context.Context, run *componentRun) (*domain.HealthReport, error) {
		credential := run.result.HeadlampCredential
		if credential == nil || credential.APIVersion != artifactAPI || credential.Kind != "ServiceCredential" || credential.Metadata.Name != "headlamp-admin" || credential.Metadata.Role != "cluster-admin" || credential.Spec.Namespace != observabilityNamespace || credential.Spec.Service != headlampRelease || credential.Spec.Username != "token" || credential.Spec.Password == "" {
			report := unhealthy("Headlamp credential is invalid", "headlampCredential", "invalid")
			return &report, nil
		}
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, nil, "get", "--raw=/api/v1/namespaces/"+observabilityNamespace+"/services/http:"+headlampRelease+":80/proxy/"); err != nil {
			report := unhealthy("Managed Headlamp endpoint is unavailable: "+err.Error(), "headlampEndpoint", "unhealthy")
			return &report, nil
		}
		if token, err := readHeadlampToken(ctx, run.runner, run.kubeconfig); err != nil || token != credential.Spec.Password {
			report := unhealthy("Managed Headlamp token does not match its protected credential", "headlampCredential", "invalid")
			return &report, nil
		}
		run.checks["headlamp"] = headlampVersion
		return nil, nil
	},
	remove: func(ctx context.Context, run *componentRun) error {
		return run.uninstall(ctx, headlampRelease, observabilityNamespace)
	},
	publish: func(builder *capabilityBuilder) {
		builder.component("headlamp", headlampVersion, observabilityNamespace)
		builder.endpoint("headlamp", observabilityNamespace, headlampRelease, headlampPort)
		builder.version("headlamp." + headlampVersion)
	},
}

func headlampValues(marker string) []byte {
	return []byte(fmt.Sprintf(`replicaCount: 1
fullnameOverride: %s
image:
  registry: ghcr.io
  repository: headlamp-k8s/headlamp
  tag: v%s
  pullPolicy: IfNotPresent
nodeSelector: {kubephos.dev/role: management}
service:
  type: NodePort
  port: 80
  nodePort: %d
serviceAccount:
  create: true
  name: %s
  annotations: {%s: %q}
clusterRoleBinding:
  create: false
config:
  enableHelm: false
  unsafeUseServiceAccountToken: false
  oidc:
    secret:
      create: false
securityContext:
  runAsNonRoot: true
  privileged: false
  runAsUser: 100
  runAsGroup: 101
  allowPrivilegeEscalation: false
  readOnlyRootFilesystem: true
  capabilities: {drop: [ALL]}
extraManifests:
- |-
  apiVersion: rbac.authorization.k8s.io/v1
  kind: ClusterRoleBinding
  metadata:
    name: %s-cluster-admin
    labels: {app.kubernetes.io/managed-by: Helm}
    annotations:
      meta.helm.sh/release-name: %s
      meta.helm.sh/release-namespace: %s
      %s: %q
  roleRef:
    apiGroup: rbac.authorization.k8s.io
    kind: ClusterRole
    name: cluster-admin
  subjects:
  - kind: ServiceAccount
    name: %s
    namespace: %s
- |-
  apiVersion: v1
  kind: Secret
  metadata:
    name: %s
    namespace: %s
    annotations:
      kubernetes.io/service-account.name: %s
      %s: %q
  type: kubernetes.io/service-account-token
`, headlampRelease, headlampVersion, headlampPort, headlampRelease, ownershipKey, marker, headlampRelease, headlampRelease, observabilityNamespace, ownershipKey, marker, headlampRelease, observabilityNamespace, headlampTokenSecret, observabilityNamespace, headlampRelease, ownershipKey, marker))
}

func readHeadlampToken(ctx context.Context, runner clusterRunner, kubeconfig string) (string, error) {
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		encoded, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "secret", headlampTokenSecret, "-n", observabilityNamespace, "-o", "jsonpath={.data.token}")
		if err == nil && strings.TrimSpace(encoded) != "" {
			decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
			if decodeErr != nil {
				return "", fmt.Errorf("decode Headlamp service account token: %w", decodeErr)
			}
			token := strings.TrimSpace(string(decoded))
			if token == "" {
				return "", errors.New("Headlamp service account token is empty")
			}
			return token, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			if lastErr != nil {
				return "", fmt.Errorf("read Headlamp service account token: %w", lastErr)
			}
			return "", errors.New("Headlamp service account token was not populated")
		case <-ticker.C:
		}
	}
}
