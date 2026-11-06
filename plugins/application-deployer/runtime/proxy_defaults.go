package applicationdeployer

import (
	"context"
	"encoding/json"
	"errors"

	"gopkg.in/yaml.v3"
)

// Read the installed injector values instead of assuming chart defaults.
func IstioProxyDefaults(ctx context.Context, kubeconfig string) (map[string]string, error) {
	raw, err := localRunner{}.Run(ctx, kubeconfig, nil, "get", "configmap", "istio-sidecar-injector", "-n", "istio-system", "-o", "json")
	if err != nil {
		return nil, err
	}
	return parseProxyDefaults([]byte(raw))
}
func parseProxyDefaults(raw []byte) (map[string]string, error) {
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(raw, &cm) != nil {
		return nil, errors.New("Istio injector configuration is invalid.")
	}
	var values struct {
		Global struct {
			Proxy struct {
				Resources struct {
					Requests map[string]string `yaml:"requests"`
					Limits   map[string]string `yaml:"limits"`
				} `yaml:"resources"`
			} `yaml:"proxy"`
		} `yaml:"global"`
	}
	if cm.Data["values"] == "" || yaml.Unmarshal([]byte(cm.Data["values"]), &values) != nil {
		return nil, errors.New("Istio injector resource defaults are unavailable.")
	}
	result := map[string]string{}
	for _, resource := range []struct{ name, field string }{{"cpu", "cpu"}, {"memory", "memory"}} {
		if v := values.Global.Proxy.Resources.Requests[resource.name]; v != "" {
			result[resource.field+"Request"] = v
		}
		if v := values.Global.Proxy.Resources.Limits[resource.name]; v != "" {
			result[resource.field+"Limit"] = v
		}
	}
	return result, nil
}
