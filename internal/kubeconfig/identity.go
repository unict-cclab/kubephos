package kubeconfig

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func WithIdentity(value, clusterName string) (string, error) {
	clusterName = strings.TrimSpace(clusterName)
	if clusterName == "" {
		return "", errors.New("cluster name is required")
	}
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(value), &document); err != nil || len(document.Content) != 1 {
		return "", errors.New("kubeconfig is invalid")
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", errors.New("kubeconfig root is invalid")
	}
	current := mappingValue(root, "current-context")
	contexts := mappingValue(root, "contexts")
	clusters := mappingValue(root, "clusters")
	users := mappingValue(root, "users")
	if current == nil || current.Kind != yaml.ScalarNode || current.Value == "" || contexts == nil || clusters == nil || users == nil {
		return "", errors.New("kubeconfig identity is incomplete")
	}
	contextEntry := namedEntry(contexts, current.Value)
	if contextEntry == nil {
		return "", errors.New("kubeconfig current context is unavailable")
	}
	contextValue := mappingValue(contextEntry, "context")
	clusterReference := mappingValue(contextValue, "cluster")
	userReference := mappingValue(contextValue, "user")
	if clusterReference == nil || userReference == nil || clusterReference.Value == "" || userReference.Value == "" {
		return "", errors.New("kubeconfig context identity is incomplete")
	}
	clusterEntry := namedEntry(clusters, clusterReference.Value)
	userEntry := namedEntry(users, userReference.Value)
	if clusterEntry == nil || userEntry == nil {
		return "", errors.New("kubeconfig context references are unavailable")
	}
	adminName := clusterName + "-admin"
	if err := setMappingScalar(clusterEntry, "name", clusterName); err != nil {
		return "", err
	}
	if err := setMappingScalar(userEntry, "name", adminName); err != nil {
		return "", err
	}
	if err := setMappingScalar(contextEntry, "name", clusterName); err != nil {
		return "", err
	}
	clusterReference.Value = clusterName
	userReference.Value = adminName
	current.Value = clusterName
	output, err := yaml.Marshal(&document)
	if err != nil {
		return "", fmt.Errorf("encode kubeconfig: %w", err)
	}
	return string(output), nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return node.Content[index+1]
		}
	}
	return nil
}

func namedEntry(sequence *yaml.Node, name string) *yaml.Node {
	if sequence == nil || sequence.Kind != yaml.SequenceNode {
		return nil
	}
	for _, entry := range sequence.Content {
		value := mappingValue(entry, "name")
		if value != nil && value.Value == name {
			return entry
		}
	}
	return nil
}

func setMappingScalar(node *yaml.Node, key, value string) error {
	target := mappingValue(node, key)
	if target == nil || target.Kind != yaml.ScalarNode {
		return fmt.Errorf("kubeconfig %s is invalid", key)
	}
	target.Value = value
	return nil
}
