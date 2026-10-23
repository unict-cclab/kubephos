package applicationdeployer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const bootstrapFixture = `apiVersion: apps/v1
kind: Deployment
metadata: {name: frontend}
spec:
  template:
    spec: {containers: [{name: frontend, image: example/frontend}]}
---
apiVersion: v1
kind: Service
metadata: {name: frontend}
spec: {ports: [{port: 80}]}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: proxy-config}
data: {upstream: frontend}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: gateway-cloud}
spec:
  template:
    spec: {nodeSelector: {kubernetes.io/hostname: cloud}, containers: [{name: proxy, image: example/proxy}]}
`

func fixedGateway() []workloadTarget {
	return []workloadTarget{{ID: "gateway-cloud", Kind: "Deployment", Name: "gateway-cloud", NodeName: "cloud", Traits: []string{trafficEntrypointTrait}, Selector: map[string]string{proxyInstanceLabel: "gateway-cloud"}}}
}

func TestGatewayCapacityIsReservedBeforeApplicationPods(t *testing.T) {
	commands := []string{}
	runner := readinessRunner(func(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
		switch args[0] {
		case "apply":
			if len(commands) == 0 {
				if strings.Contains(string(stdin), "image: example/frontend") || !strings.Contains(string(stdin), "name: gateway-cloud") || !strings.Contains(string(stdin), "kind: Service") || !strings.Contains(string(stdin), "kind: ConfigMap") {
					t.Fatalf("proxy bootstrap prerequisites are wrong: %s", stdin)
				}
				commands = append(commands, "bootstrap")
			} else {
				if len(commands) != 2 || commands[1] != "scheduled" {
					t.Fatalf("application started before proxy scheduling: %v", commands)
				}
				if !strings.Contains(string(stdin), "image: example/frontend") || strings.Contains(string(stdin), "name: gateway-cloud") {
					t.Fatalf("application phase is wrong: %s", stdin)
				}
				commands = append(commands, "application")
			}
		case "wait":
			if args[1] != "--for=condition=PodScheduled" {
				t.Fatalf("bootstrap must not wait for upstream-dependent readiness: %v", args)
			}
			if !strings.Contains(strings.Join(args, " "), proxyInstanceLabel+"=gateway-cloud") {
				t.Fatalf("wrong proxy selector: %v", args)
			}
			commands = append(commands, "scheduled")
		default:
			t.Fatalf("unexpected command: %v", args)
		}
		return "ok", nil
	})
	if err := applyRuntimeManifest(context.Background(), runner, "config", "namespace", bootstrapFixture, fixedGateway(), discardLog); err != nil {
		t.Fatal(err)
	}
	if strings.Join(commands, ",") != "bootstrap,scheduled,application" {
		t.Fatalf("wrong deployment order: %v", commands)
	}
}

func TestUnavailableGatewayDoesNotStartApplicationPods(t *testing.T) {
	cause := errors.New("insufficient cpu")
	applications := 0
	runner := readinessRunner(func(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
		switch args[0] {
		case "apply":
			applications++
			return "", nil
		case "wait":
			return "", cause
		case "get":
			return "", errors.New("diagnostics unavailable")
		}
		t.Fatalf("unexpected command %v", args)
		return "", nil
	})
	err := applyRuntimeManifest(context.Background(), runner, "config", "namespace", bootstrapFixture, fixedGateway(), discardLog)
	if !errors.Is(err, cause) || applications != 1 || !strings.Contains(err.Error(), "node cloud") {
		t.Fatalf("blocked gateway did not stop the deployment: %v applies=%d", err, applications)
	}
}

func TestApplicationsWithoutFixedGatewaysKeepOneManifestApply(t *testing.T) {
	calls := 0
	runner := readinessRunner(func(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
		calls++
		if args[0] != "apply" || string(stdin) != bootstrapFixture {
			t.Fatalf("ordinary deployment changed: %v", args)
		}
		return "ok", nil
	})
	if err := applyRuntimeManifest(context.Background(), runner, "config", "namespace", bootstrapFixture, nil, discardLog); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("expected one apply, got %d", calls)
	}
}

func TestMissingFixedGatewayFailsBeforeApplyingAnything(t *testing.T) {
	workloads := fixedGateway()
	workloads[0].Name = "missing"
	runner := readinessRunner(func(context.Context, string, []byte, ...string) (string, error) {
		t.Fatal("invalid bootstrap was applied")
		return "", nil
	})
	if err := applyRuntimeManifest(context.Background(), runner, "config", "namespace", bootstrapFixture, workloads, discardLog); err == nil {
		t.Fatal("missing gateway was accepted")
	}
}

func TestGatewayOnlyManifestHasNoEmptyApplicationPhase(t *testing.T) {
	content := `apiVersion: apps/v1
kind: Deployment
metadata: {name: gateway-cloud}
spec: {replicas: 1}
`
	bootstrap, application, gateways, err := partitionTrafficBootstrap(content, fixedGateway())
	if err != nil || bootstrap == "" || application != "" || len(gateways) != 1 {
		t.Fatalf("unexpected gateway-only partition: bootstrap=%q application=%q gateways=%d err=%v", bootstrap, application, len(gateways), err)
	}
}

func TestRedisBootstrapJobsGateReadiness(t *testing.T) {
	content := `apiVersion: batch/v1
kind: Job
metadata:
  name: redis-bootstrap
  annotations: {kubephos.dev/bootstrap: "true"}
---
apiVersion: batch/v1
kind: Job
metadata: {name: other-job}
`
	calls := 0
	cause := errors.New("bootstrap failed")
	runner := readinessRunner(func(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
		calls++
		if strings.Join(args, " ") != "wait --for=condition=Complete job/redis-bootstrap -n experiment --timeout=10m" {
			t.Fatalf("unexpected bootstrap gate %v", args)
		}
		return "", cause
	})
	if err := verifyBootstrapJobs(context.Background(), runner, "config", "experiment", content); !errors.Is(err, cause) || calls != 1 {
		t.Fatalf("bootstrap failure was ignored: %v, calls %d", err, calls)
	}
	if !nativeTCPEndpoints([]serviceEndpoint{{Protocol: "tcp"}}) || nativeTCPEndpoints(nil) || nativeTCPEndpoints([]serviceEndpoint{{Protocol: "http"}, {Protocol: "tcp"}}) {
		t.Fatal("incorrect native namespace injection policy")
	}
}
