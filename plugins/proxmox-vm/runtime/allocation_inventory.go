package proxmoxvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// AllocationVM contains only addressing information; credentials never leave the client.
type AllocationVM struct {
	VMID      int
	Node      string
	Config    map[string]any
	Addresses []string
}

// ReadAllocationInventory includes QEMU guests and containers on every Proxmox node.
func ReadAllocationInventory(ctx context.Context, endpoint string, verifyTLS bool, tokenID, tokenSecret string) ([]AllocationVM, error) {
	raw, _ := json.Marshal(credential{TokenID: tokenID, TokenSecret: tokenSecret})
	c, err := newClient(endpoint, verifyTLS, "inventory", map[string]json.RawMessage{"inventory": raw})
	if err != nil {
		return nil, err
	}
	defer c.http.CloseIdleConnections()
	resources, err := c.resources(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]AllocationVM, len(resources))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstError error
	slots := make(chan struct{}, 6)
	for i, resource := range resources {
		if resource.Type != "qemu" && resource.Type != "lxc" {
			continue
		}
		wg.Add(1)
		go func(i int, resource vmResource) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				firstError = ctx.Err()
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			item := AllocationVM{VMID: resource.VMID, Node: resource.Node}
			path := "/api2/json/nodes/" + url.PathEscape(resource.Node) + "/" + resource.Type + "/" + strconv.Itoa(resource.VMID)
			err := c.request(ctx, http.MethodGet, path+"/config", nil, &item.Config)
			if err != nil {
				mu.Lock()
				if firstError == nil {
					firstError = fmt.Errorf("could not read VM %d network configuration", resource.VMID)
				}
				mu.Unlock()
				return
			}
			if resource.Type == "qemu" && resource.Status == "running" {
				var agent struct {
					Result []struct {
						Addresses []struct {
							Address string `json:"ip-address"`
						} `json:"ip-addresses"`
					} `json:"result"`
				}
				// Guest agents are optional. Static cloud-init addressing remains available without one.
				agentCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				if c.request(agentCtx, http.MethodGet, path+"/agent/network-get-interfaces", nil, &agent) == nil {
					for _, iface := range agent.Result {
						for _, address := range iface.Addresses {
							if !strings.HasPrefix(address.Address, "127.") {
								item.Addresses = append(item.Addresses, address.Address)
							}
						}
					}
				}
				cancel()
			}
			result[i] = item
		}(i, resource)
	}
	wg.Wait()
	if firstError != nil {
		return nil, firstError
	}
	return result, nil
}
