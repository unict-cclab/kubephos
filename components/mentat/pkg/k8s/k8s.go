package k8s

import (
	"context"
	"fmt"
	"os"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

type Node struct {
	Hostname string
	Ip       string
}

func GetPeerList() ([]Node, error) {
	var simpleList []Node

	config, err := rest.InClusterConfig()
	if err != nil {
		return simpleList, err
	}

	c, err := kubernetes.NewForConfig(config)
	if err != nil {
		return simpleList, err
	}

	namespace := strings.TrimSpace(os.Getenv("POD_NAMESPACE"))
	if namespace == "" {
		data, readErr := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
		if readErr != nil {
			return simpleList, fmt.Errorf("determining pod namespace: %w", readErr)
		}
		namespace = strings.TrimSpace(string(data))
	}

	pods, err := c.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=mentat",
		Limit:         500,
	})
	if err != nil {
		return simpleList, err
	}

	for _, item := range pods.Items {
		node, ok := peerFromPod(item.Spec.NodeName, item.Status.PodIP)
		if !ok {
			continue
		}
		simpleList = append(simpleList, node)
	}

	return simpleList, nil
}

func peerFromPod(nodeName, podIP string) (Node, bool) {
	if nodeName == "" || podIP == "" {
		return Node{}, false
	}
	return Node{Hostname: nodeName, Ip: podIP}, true
}

func GetNodeName() (string, error) {
	name := os.Getenv("NODE_NAME")

	var err error
	if name == "" {
		name, err = os.Hostname()
	}

	return name, err

}
