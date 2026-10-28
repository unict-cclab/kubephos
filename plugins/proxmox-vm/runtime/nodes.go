package proxmoxvm

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
)

type ProxmoxNode struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
}

func ReadProxmoxNodes(ctx context.Context, endpoint string, verifyTLS bool, tokenID, tokenSecret string) ([]ProxmoxNode, error) {
	raw, _ := json.Marshal(credential{TokenID: tokenID, TokenSecret: tokenSecret})
	client, err := newClient(endpoint, verifyTLS, "nodes", map[string]json.RawMessage{"nodes": raw})
	if err != nil {
		return nil, errors.New("could not connect to Proxmox")
	}
	defer client.http.CloseIdleConnections()
	nodes, err := client.nodes(ctx)
	if err != nil {
		return nil, errors.New("could not read Proxmox servers")
	}
	result := []ProxmoxNode{}
	for _, node := range nodes {
		if regexpNode.MatchString(node.Node) {
			result = append(result, ProxmoxNode{Name: node.Node, Online: node.Status == "online"})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}
