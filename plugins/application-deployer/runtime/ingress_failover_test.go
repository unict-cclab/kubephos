package applicationdeployer

import (
	"strings"
	"testing"
)

const ingressFailoverFixture = `apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: ingress
spec:
  template:
    spec:
      containers:
        - name: proxy
          image: registry.example/cache/nginx:1.27-alpine
          volumeMounts:
            - name: existing
              mountPath: /etc/nginx/templates/default.conf.template
              subPath: default.conf.template
      volumes:
        - name: existing
          configMap:
            name: existing
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: existing
data:
  default.conf.template: |
    upstream peers {
      server arbitrary-service:8080;
      keepalive 64;
    }
    server {
      listen 8080;
      location / {
        proxy_pass http://peers;
      }
    }
`

func failoverWorkloads() []workloadTarget {
	return []workloadTarget{{ID: "entry", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "ingress", Traits: []string{trafficEntrypointTrait}}}
}

func TestIngressFailoverIsInheritedByExpandedGateways(t *testing.T) {
	content, err := configureIngressFailover(ingressFailoverFixture, failoverWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	content, _, err = expandTrafficEntrypoints(content, failoverWorkloads(), []serviceEndpoint{{Component: "entry", Service: "public"}}, []applicationNode{{Name: "a", Hostname: "a", Zone: "east"}, {Name: "b", Hostname: "b", Zone: "west"}})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := decodeManifestResources(content)
	if err != nil {
		t.Fatal(err)
	}
	gateways, policies := 0, 0
	for _, r := range resources {
		meta, _ := r["metadata"].(map[string]any)
		if r["kind"] == "Deployment" {
			gateways++
			spec := r["spec"].(map[string]any)
			template := spec["template"].(map[string]any)
			pod := template["spec"].(map[string]any)
			container := pod["containers"].([]any)[0].(map[string]any)
			mounts := container["volumeMounts"].([]any)
			if len(mounts) != 2 || len(pod["volumes"].([]any)) != 2 {
				t.Fatalf("gateway lost mounts or policy: %+v", pod)
			}
			mount := mounts[1].(map[string]any)
			if mount["mountPath"] != ingressFailoverPath || mount["readOnly"] != true {
				t.Fatalf("incorrect policy mount: %+v", mount)
			}
		}
		if r["kind"] == "ConfigMap" && meta["name"] == ingressFailoverName {
			policies++
			policy := r["data"].(map[string]any)["failover.conf"].(string)
			for _, required := range []string{"error timeout invalid_header http_502 http_503 http_504;", "proxy_next_upstream_tries 2;", "proxy_next_upstream_timeout 5s;"} {
				if !strings.Contains(policy, required) {
					t.Fatalf("missing %q in policy %q", required, policy)
				}
			}
			if strings.Contains(policy, "non_idempotent") || strings.Contains(policy, "http_500") {
				t.Fatalf("unsafe retry policy: %s", policy)
			}
		}
		if r["kind"] == "ConfigMap" && meta["name"] == "existing" {
			conf := r["data"].(map[string]any)["default.conf.template"].(string)
			if !strings.Contains(conf, "keepalive 64;") || !strings.Contains(conf, "arbitrary-service:8080") {
				t.Fatal("original configuration changed")
			}
		}
	}
	if gateways != 2 || policies != 1 {
		t.Fatalf("gateways=%d policies=%d", gateways, policies)
	}
}

func TestIngressFailoverIsIdempotent(t *testing.T) {
	first, err := configureIngressFailover(ingressFailoverFixture, failoverWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	second, err := configureIngressFailover(first, failoverWorkloads())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("applying policy twice changed manifests")
	}
}

func TestIngressFailoverDoesNotModifyOtherWorkloads(t *testing.T) {
	for _, tt := range []struct {
		name, content string
		workloads     []workloadTarget
	}{
		{"not an entrypoint", ingressFailoverFixture, []workloadTarget{{ID: "entry", Kind: "DaemonSet", Name: "ingress"}}},
		{"different proxy", strings.ReplaceAll(ingressFailoverFixture, "registry.example/cache/nginx:1.27-alpine", "envoyproxy/envoy:v1.30"), failoverWorkloads()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configureIngressFailover(tt.content, tt.workloads)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.content {
				t.Fatal("unrelated workload changed")
			}
		})
	}
}

func TestIngressFailoverRejectsConflictingResources(t *testing.T) {
	for _, content := range []string{
		ingressFailoverFixture + "---\napiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: kubephos-ingress-failover\ndata:\n  failover.conf: custom\n",
		strings.Replace(ingressFailoverFixture, "- name: existing\n              mountPath: /etc/nginx/templates/default.conf.template", "- name: existing\n              mountPath: "+ingressFailoverPath, 1),
		strings.Replace(ingressFailoverFixture, "- name: existing\n          configMap:", "- name: "+ingressFailoverName+"\n          configMap:", 1),
	} {
		if _, err := configureIngressFailover(content, failoverWorkloads()); err == nil {
			t.Fatal("expected conflict to be rejected")
		}
	}
}
