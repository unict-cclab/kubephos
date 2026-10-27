package k3s

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const ciliumVersion = "1.20.3"
const gatewayAPIVersion = "v1.6.3"

func validateNetworking(spec Spec) error {
	if spec.Networking == "" || spec.Networking == "flannel" {
		if spec.ClusterID != 0 || spec.PodCIDR != "" || spec.ServiceCIDR != "" {
			return errors.New("cluster identity and CIDRs require Cilium networking")
		}
		return nil
	}
	if spec.Networking != "cilium" || spec.ClusterID < 1 || spec.ClusterID > 255 {
		return errors.New("Cilium requires a cluster ID between 1 and 255")
	}
	if !regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`).MatchString(spec.ClusterName) {
		return errors.New("Cilium cluster names must start and end with a lowercase letter or number")
	}
	pod, err := netip.ParsePrefix(spec.PodCIDR)
	if err != nil || !pod.Addr().Is4() || pod != pod.Masked() || pod.Bits() > 24 || pod.Bits() < 16 {
		return errors.New("use an aligned IPv4 pod CIDR with prefix between 16 and 24")
	}
	service, err := netip.ParsePrefix(spec.ServiceCIDR)
	if err != nil || !service.Addr().Is4() || service != service.Masked() || service.Bits() > 24 || service.Bits() < 16 || pod.Overlaps(service) {
		return errors.New("use a separate aligned IPv4 service CIDR with prefix between 16 and 24")
	}
	return nil
}

func installCommandForSpec(spec Spec, role, nodeName, server, token string, first bool, labels map[string]string) string {
	command := installCommandWithLabels(role, nodeName, server, token, first, labels)
	if spec.Networking == "cilium" && role == "server" {
		dns, _ := netip.ParsePrefix(spec.ServiceCIDR)
		dnsAddress := dns.Addr()
		for i := 0; i < 10; i++ {
			dnsAddress = dnsAddress.Next()
		}
		command += " --cluster-dns=" + shellQuote(dnsAddress.String()) + " --flannel-backend=none --disable-network-policy --disable-kube-proxy --disable=traefik --disable=servicelb --cluster-cidr=" + shellQuote(spec.PodCIDR) + " --service-cidr=" + shellQuote(spec.ServiceCIDR)
	}
	return command
}

func ciliumBootstrapManifest(spec Spec, host string) ([]byte, error) {
	values := map[string]any{
		"kubeProxyReplacement": true, "k8sServiceHost": host, "k8sServicePort": 6443,
		"cluster": map[string]any{"name": spec.ClusterName, "id": spec.ClusterID},
		"ipam":    map[string]any{"mode": "kubernetes"}, "routingMode": "tunnel", "tunnelProtocol": "vxlan",
		"gatewayAPI":  map[string]any{"enabled": true},
		"clustermesh": map[string]any{"useAPIServer": true, "config": map[string]any{"enabled": true}, "enableEndpointSliceSynchronization": true, "apiserver": map[string]any{"nodeSelector": map[string]string{"kubephos.dev/role": "management"}, "service": map[string]any{"type": "NodePort", "nodePort": 32379}}},
		"hubble":      map[string]any{"enabled": true, "relay": map[string]any{"enabled": true, "nodeSelector": map[string]string{"kubephos.dev/role": "management"}}, "ui": map[string]any{"enabled": true, "nodeSelector": map[string]string{"kubephos.dev/role": "management"}, "service": map[string]any{"type": "NodePort", "nodePort": 31235}}, "metrics": map[string]any{"enabled": []string{"dns", "drop", "tcp", "flow", "icmp", "httpV2"}}},
		"operator":    map[string]any{"replicas": 1, "nodeSelector": map[string]string{"kubephos.dev/role": "management"}},
		"prometheus":  map[string]any{"enabled": true},
	}
	raw, err := yaml.Marshal(values)
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(map[string]any{"apiVersion": "helm.cattle.io/v1", "kind": "HelmChart", "metadata": map[string]string{"name": "cilium", "namespace": "kube-system"}, "spec": map[string]any{"repo": "https://helm.cilium.io/", "chart": "cilium", "version": ciliumVersion, "targetNamespace": "kube-system", "bootstrap": true, "valuesContent": string(raw)}})
}
func installCilium(ctx context.Context, runner commandRunner, primary machine, access machineAccess, spec Spec) error {
	manifest, err := ciliumBootstrapManifest(spec, primary.Address)
	if err != nil {
		return err
	}
	// K3s' bootstrap Helm controller runs with host networking before the CNI is ready.
	command := "set -eu\n"
	for _, crd := range []string{"gatewayclasses", "gateways", "httproutes", "referencegrants", "grpcroutes", "backendtlspolicies", "tlsroutes", "tcproutes", "udproutes"} {
		command += "sudo k3s kubectl apply --server-side -f " + shellQuote("https://raw.githubusercontent.com/kubernetes-sigs/gateway-api/"+gatewayAPIVersion+"/config/crd/standard/gateway.networking.k8s.io_"+crd+".yaml") + "\n"
	}
	command += "printf %s " + shellQuote(base64.StdEncoding.EncodeToString(manifest)) + " | base64 -d | sudo tee /var/lib/rancher/k3s/server/manifests/cilium.yaml >/dev/null\n"
	command += "timeout 600 sh -c 'until sudo k3s kubectl -n kube-system get daemonset cilium >/dev/null 2>&1; do sleep 3; done'\n"
	command += "sudo k3s kubectl -n kube-system rollout status daemonset/cilium --timeout=10m\n"
	command += "sudo k3s kubectl -n kube-system rollout status deployment/clustermesh-apiserver --timeout=10m\n"

	command += "sudo k3s kubectl apply -f - <<'KUBEPHOS_GATEWAY'\napiVersion: cilium.io/v2alpha1\nkind: CiliumGatewayClassConfig\nmetadata:\n  name: kubephos-nodeport\n  namespace: kube-system\nspec:\n  service:\n    type: NodePort\n---\napiVersion: gateway.networking.k8s.io/v1\nkind: GatewayClass\nmetadata:\n  name: kubephos-cilium\nspec:\n  controllerName: io.cilium/gateway-controller\n  parametersRef:\n    group: cilium.io\n    kind: CiliumGatewayClassConfig\n    name: kubephos-nodeport\n    namespace: kube-system\nKUBEPHOS_GATEWAY\n"
	if _, err = runner.Run(ctx, primary, access, command); err != nil {
		return fmt.Errorf("install Cilium and Gateway API: %w", err)
	}
	return nil
}
func ciliumPoolLabels(spec Spec, labels map[string]string, poolLabels map[string]string) map[string]string {
	if labels == nil {
		labels = map[string]string{}
	}
	for key, value := range poolLabels {
		labels[key] = value
	}
	if spec.Networking == "cilium" {
		labels["kubephos.dev/cluster"] = spec.ClusterName
		delete(labels, "topology.kubernetes.io/zone")
	}
	return labels
}
func validPoolLabels(labels map[string]string) bool {
	name := regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9_.-]{0,61}[A-Za-z0-9])?$`)
	for key, value := range labels {
		if strings.HasPrefix(key, "kubephos.dev/") || strings.HasPrefix(key, "kubernetes.io/") || strings.HasPrefix(key, "node-role.kubernetes.io/") || strings.HasPrefix(key, "topology.kubernetes.io/") {
			return false
		}
		parts := strings.Split(key, "/")
		if len(parts) > 2 || !name.MatchString(parts[len(parts)-1]) || len(value) > 63 || value != "" && !name.MatchString(value) {
			return false
		}
		if len(parts) == 2 && !regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`).MatchString(parts[0]) {
			return false
		}
	}
	return true
}

func nodePlacementLabel(labels map[string]string) string {
	if cluster := labels["kubephos.dev/cluster"]; cluster != "" {
		return cluster
	}
	return labels["topology.kubernetes.io/zone"]
}
