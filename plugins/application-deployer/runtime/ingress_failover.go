package applicationdeployer

import (
	"fmt"
	"path"
	"strings"
)

const ingressFailoverName = "kubephos-ingress-failover"
const ingressFailoverPath = "/etc/nginx/conf.d/00-kubephos-failover.conf"
const ingressFailoverConfig = "proxy_next_upstream error timeout invalid_header http_502 http_503 http_504;\nproxy_next_upstream_tries 2;\nproxy_next_upstream_timeout 5s;\n"

func configureIngressFailover(content string, workloads []workloadTarget) (string, error) {
	targets := map[string]bool{}
	for _, workload := range trafficEntrypoints(workloads) {
		targets[workload.Kind+"/"+workload.Name] = true
	}
	if len(targets) == 0 {
		return content, nil
	}
	resources, err := decodeManifestResources(content)
	if err != nil {
		return "", err
	}
	enabled := false
	for _, resource := range resources {
		metadata, _ := resource["metadata"].(map[string]any)
		if !targets[fmt.Sprint(resource["kind"])+"/"+fmt.Sprint(metadata["name"])] {
			continue
		}
		spec, _ := resource["spec"].(map[string]any)
		template, _ := spec["template"].(map[string]any)
		podSpec, _ := template["spec"].(map[string]any)
		containers, _ := podSpec["containers"].([]any)
		matched := false
		for _, value := range containers {
			container, _ := value.(map[string]any)
			image := strings.SplitN(fmt.Sprint(container["image"]), "@", 2)[0]
			image = strings.SplitN(path.Base(image), ":", 2)[0]
			if image != "nginx" {
				continue
			}
			mounts, _ := container["volumeMounts"].([]any)
			exists := false
			for _, value := range mounts {
				mount, _ := value.(map[string]any)
				if mount["mountPath"] == ingressFailoverPath {
					if mount["name"] != ingressFailoverName || mount["subPath"] != "failover.conf" {
						return "", fmt.Errorf("traffic entrypoint %s already mounts %s", metadata["name"], ingressFailoverPath)
					}
					exists = true
				}
			}
			if !exists {
				container["volumeMounts"] = append(mounts, map[string]any{"name": ingressFailoverName, "mountPath": ingressFailoverPath, "subPath": "failover.conf", "readOnly": true})
			}
			matched = true
		}
		if !matched {
			continue
		}
		volumes, _ := podSpec["volumes"].([]any)
		exists := false
		for _, value := range volumes {
			volume, _ := value.(map[string]any)
			if volume["name"] == ingressFailoverName {
				config, _ := volume["configMap"].(map[string]any)
				if config["name"] != ingressFailoverName {
					return "", fmt.Errorf("traffic entrypoint %s already uses volume %s", metadata["name"], ingressFailoverName)
				}
				exists = true
			}
		}
		if !exists {
			podSpec["volumes"] = append(volumes, map[string]any{"name": ingressFailoverName, "configMap": map[string]any{"name": ingressFailoverName}})
		}
		enabled = true
	}
	if !enabled {
		return content, nil
	}
	exists := false
	for _, resource := range resources {
		metadata, _ := resource["metadata"].(map[string]any)
		if resource["kind"] != "ConfigMap" || metadata["name"] != ingressFailoverName {
			continue
		}
		data, _ := resource["data"].(map[string]any)
		labels, _ := metadata["labels"].(map[string]any)
		if data["failover.conf"] != ingressFailoverConfig || labels["app.kubernetes.io/managed-by"] != "kubephos" {
			return "", fmt.Errorf("ConfigMap %s conflicts with ingress failover configuration", ingressFailoverName)
		}
		exists = true
	}
	if !exists {
		resources = append(resources, map[string]any{
			"apiVersion": "v1", "kind": "ConfigMap",
			"metadata": map[string]any{"name": ingressFailoverName, "labels": map[string]any{"app.kubernetes.io/managed-by": "kubephos"}},
			"data":     map[string]any{"failover.conf": ingressFailoverConfig},
		})
	}
	return encodeManifestResources(resources)
}
