package applicationdeployer

import (
	"fmt"
	"strings"
	"testing"
)

const ciliumApplication = `apiVersion: v1
kind: ConfigMap
metadata: {name: node-proxy}
data:
  default.conf: |
    server { location / { proxy_pass http://s0:80/api/v1; } }
---
apiVersion: apps/v1
kind: DaemonSet
metadata: {name: node-proxy}
spec:
  selector: {matchLabels: {app: node-proxy}}
  template:
    metadata: {labels: {app: node-proxy}}
    spec:
      containers: [{name: nginx, image: nginx}]
      volumes: [{name: config, configMap: {name: node-proxy}}]
---
apiVersion: v1
kind: Service
metadata: {name: node-proxy}
spec: {type: NodePort, selector: {app: node-proxy}, ports: [{port: 80, targetPort: 8080}]}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: s0}
spec:
  selector: {matchLabels: {app: s0}}
  template:
    metadata: {labels: {app: s0}}
    spec: {containers: [{name: s0, image: test/application}]}
---
apiVersion: v1
kind: Service
metadata: {name: s0}
spec: {selector: {app: s0}, ports: [{port: 80, targetPort: 8080}]}
`

func TestCiliumGatewayReplacesNodeProxiesAndPreservesBackendRouting(t *testing.T) {
	workloads := []workloadTarget{{ID: "node-proxy", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy", Traits: []string{trafficEntrypointTrait}}, {ID: "s0", APIVersion: "apps/v1", Kind: "Deployment", Name: "s0", Traits: []string{"schedulable"}}}
	endpoints := []serviceEndpoint{{ID: "http", Component: "node-proxy", Service: "node-proxy", Protocol: "http", Port: 80, Path: "/"}}
	content, targets, converted, err := ciliumGatewayPackage(ciliumApplication, workloads, endpoints, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Name != "s0" || converted[0].Service != "cilium-gateway-entry-http" {
		t.Fatalf("invalid Cilium deployment %v %v", targets, converted)
	}
	for _, forbidden := range []string{"kind: DaemonSet", "proxy_pass", "image: nginx", "resources:"} {
		if strings.Contains(content, forbidden) {
			t.Fatalf("unexpected %s", forbidden)
		}
	}
	for _, required := range []string{"kind: Gateway", "kind: HTTPRoute", "gatewayClassName: kubephos-cilium", "replacePrefixMatch: /api/v1", "name: s0", "service.cilium.io/global:"} {
		if !strings.Contains(content, required) {
			t.Fatalf("missing %s in %s", required, content)
		}
	}
}

func TestCiliumGatewaySplitsLargeRouteSetsAndBoundsServiceNames(t *testing.T) {
	config := "server { "
	for i := 0; i < 35; i++ {
		config += fmt.Sprintf("location /service-%d { proxy_pass http://s0:80; } ", i)
	}
	config += "}"
	content := strings.Replace(ciliumApplication, "server { location / { proxy_pass http://s0:80/api/v1; } }", config, 1)
	workloads := []workloadTarget{{ID: "node-proxy", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy", Traits: []string{trafficEntrypointTrait}}, {ID: "s0", APIVersion: "apps/v1", Kind: "Deployment", Name: "s0"}}
	raw, _, endpoints, err := ciliumGatewayPackage(content, workloads, []serviceEndpoint{{ID: strings.Repeat("a", 63), Component: "node-proxy", Service: "node-proxy", Protocol: "http", Port: 80}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints[0].Service) > 63 {
		t.Fatal("gateway service name exceeds DNS limit")
	}
	resources, err := decodeManifestResources(raw)
	if err != nil {
		t.Fatal(err)
	}
	routes, total := 0, 0
	for _, resource := range resources {
		if resource["kind"] != "HTTPRoute" {
			continue
		}
		rules := resource["spec"].(map[string]any)["rules"].([]any)
		if len(rules) > 16 {
			t.Fatal("HTTPRoute has too many rules")
		}
		routes++
		total += len(rules)
	}
	if routes != 3 || total != 35 {
		t.Fatalf("lost routes: %d resources, %d rules", routes, total)
	}
}
