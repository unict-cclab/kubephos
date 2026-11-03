package applicationdeployer

import (
	"errors"
	"regexp"
	"strings"
)

var errNamespaceNotOwned = errors.New("application namespace is not owned by this operation")

// Explicit namespaces are disposable application resources, never platform namespaces.
func ValidateApplicationNamespace(name string) error {
	if name == "" {
		return nil
	}
	if len(name) > 63 || !regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(name) {
		return errors.New("Namespace must contain up to 63 lowercase letters, digits or hyphens and start and end with a letter or digit.")
	}
	protected := map[string]bool{"default": true, "istio-system": true, "chaos-mesh": true, "cilium": true, "cilium-system": true, "cert-manager": true, "metallb-system": true, "ingress-nginx": true, "monitoring": true, "prometheus": true, "local-path-storage": true}
	if protected[name] || strings.HasPrefix(name, "kube-") || strings.HasPrefix(name, "kubephos-") {
		return errors.New("Choose an application namespace; system and platform namespaces cannot be recreated.")
	}
	return nil
}

func resetApplicationNamespace(input stepInput) bool {
	return input.ApplicationNamespace != "" && input.Mode != "deploy"
}
