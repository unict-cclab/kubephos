package mesh

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

func configureMeshFederation(ctx context.Context, runner Runner, source string, configs map[string]string) error {
	config := configs[source]
	raw, err := runner.Kubectl(ctx, config, nil, "get", "prometheus", "-n", "kubephos-observability", "-o", "json")
	if err != nil {
		return err
	}
	var prometheus struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				Additional struct {
					Name string `json:"name"`
					Key  string `json:"key"`
				} `json:"additionalScrapeConfigs"`
			} `json:"spec"`
		} `json:"items"`
	}
	if json.Unmarshal([]byte(raw), &prometheus) != nil || len(prometheus.Items) != 1 {
		return errors.New("expected one managed Prometheus instance")
	}
	item := prometheus.Items[0]
	jobs := []map[string]any{}
	if item.Spec.Additional.Name != "" {
		secretRaw, err := runner.Kubectl(ctx, config, nil, "get", "secret", item.Spec.Additional.Name, "-n", "kubephos-observability", "-o", "json")
		if err != nil {
			return err
		}
		var secret struct {
			Data map[string]string `json:"data"`
		}
		if json.Unmarshal([]byte(secretRaw), &secret) != nil {
			return errors.New("invalid Prometheus scrape configuration")
		}
		value, err := base64.StdEncoding.DecodeString(secret.Data[item.Spec.Additional.Key])
		if err != nil {
			return err
		}
		if err := yaml.Unmarshal(value, &jobs); err != nil {
			return err
		}
	}
	filtered := []map[string]any{}
	for _, job := range jobs {
		name, _ := job["job_name"].(string)
		if !strings.HasPrefix(name, "kubephos-mesh-") {
			filtered = append(filtered, job)
		}
	}
	for name, raw := range configs {
		if name == source {
			continue
		}
		// Use the API server identity from the verified kubeconfig, never from user-supplied URLs.
		var kube struct {
			Clusters []struct {
				Cluster struct {
					Server string `yaml:"server"`
				} `yaml:"cluster"`
			} `yaml:"clusters"`
		}
		if yaml.Unmarshal([]byte(raw), &kube) != nil || len(kube.Clusters) != 1 {
			return errors.New("invalid peer kubeconfig")
		}
		endpoint, err := url.Parse(kube.Clusters[0].Cluster.Server)
		if err != nil || endpoint.Hostname() == "" {
			return errors.New("invalid peer API server")
		}
		filtered = append(filtered, map[string]any{"job_name": "kubephos-mesh-" + name, "honor_labels": true, "metrics_path": "/federate", "params": map[string][]string{"match[]": {fmt.Sprintf(`{__name__=~"cluster_network_.*",measurement_cluster=%q}`, name)}}, "static_configs": []map[string]any{{"targets": []string{net.JoinHostPort(endpoint.Hostname(), "32090")}}}})
	}
	value, err := yaml.Marshal(filtered)
	if err != nil {
		return err
	}
	secret, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": "kubephos-mesh-scrape", "namespace": "kubephos-observability", "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos"}}, "stringData": map[string]string{"scrape.yaml": string(value)}})
	if _, err := runner.Kubectl(ctx, config, secret, "apply", "-f", "-"); err != nil {
		return err
	}
	patch := `{"spec":{"additionalScrapeConfigs":{"name":"kubephos-mesh-scrape","key":"scrape.yaml"}}}`
	_, err = runner.Kubectl(ctx, config, nil, "patch", "prometheus", item.Metadata.Name, "-n", "kubephos-observability", "--type=merge", "--patch", patch)
	return err
}
