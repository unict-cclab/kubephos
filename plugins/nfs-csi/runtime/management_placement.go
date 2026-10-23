package nfscsi

import (
	"bytes"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
)

func managementControllerManifest(raw []byte) ([]byte, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if doc == nil {
			continue
		}
		if doc["kind"] == "Deployment" {
			metadata, _ := doc["metadata"].(map[string]any)
			if metadata["name"] == "csi-nfs-controller" {
				spec, _ := doc["spec"].(map[string]any)
				template, _ := spec["template"].(map[string]any)
				pod, _ := template["spec"].(map[string]any)
				if pod == nil {
					return nil, errors.New("CSI controller deployment has no pod template")
				}
				selector, ok := pod["nodeSelector"].(map[string]any)
				if !ok {
					selector = map[string]any{}
				}
				selector["kubephos.dev/role"] = "management"
				pod["nodeSelector"] = selector
			}
		}
		if err := encoder.Encode(doc); err != nil {
			return nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
