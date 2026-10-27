package proxmoxvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNetworkAllocationsUnavailable = errors.New("VM network information is unavailable")

// AllocationVM contains only addressing information; credentials never leave the client.
type AllocationVM struct {
	VMID      int
	Node      string
	Config    map[string]any
	Addresses []string
}

// ReadAllocationVMIDs reserves identifiers across the whole cluster, including
// guests on offline nodes, without requiring their network configurations.
func ReadAllocationVMIDs(ctx context.Context, endpoint string, verifyTLS bool, tokenID, tokenSecret string) ([]AllocationVM, error) {
	c, resources, err := allocationResources(ctx, endpoint, verifyTLS, tokenID, tokenSecret)
	if err != nil {
		return nil, err
	}
	defer c.http.CloseIdleConnections()
	result := []AllocationVM{}
	for _, resource := range resources {
		if resource.Type == "qemu" || resource.Type == "lxc" {
			result = append(result, AllocationVM{VMID: resource.VMID, Node: resource.Node})
		}
	}
	return result, nil
}
func allocationResources(ctx context.Context, endpoint string, verifyTLS bool, tokenID, tokenSecret string) (*client, []vmResource, error) {
	raw, _ := json.Marshal(credential{TokenID: tokenID, TokenSecret: tokenSecret})
	c, err := newClient(endpoint, verifyTLS, "inventory", map[string]json.RawMessage{"inventory": raw})
	if err != nil {
		return nil, nil, allocationReadError{cause: err}
	}
	resources, err := c.resources(ctx)
	if err == nil && resources == nil {
		err = errors.New("empty Proxmox inventory response")
	}
	if err != nil {
		c.http.CloseIdleConnections()
		return nil, nil, allocationReadError{cause: err}
	}
	return c, resources, nil
}

// ReadAllocationInventory reads network data from available nodes and retains
// all VMIDs, including guests on offline nodes. Authentication, permission and
// invalid-data errors remain fatal; unavailable nodes do not block other nodes.
func ReadAllocationInventory(ctx context.Context, endpoint string, verifyTLS bool, tokenID, tokenSecret string) ([]AllocationVM, error) {
	c, resources, err := allocationResources(ctx, endpoint, verifyTLS, tokenID, tokenSecret)
	if err != nil {
		return nil, err
	}
	defer c.http.CloseIdleConnections()
	result := make([]AllocationVM, len(resources))
	offline := map[string]bool{}
	if nodes, nodeErr := c.nodes(ctx); nodeErr == nil {
		for _, node := range nodes {
			if node.Status == "offline" {
				offline[node.Node] = true
			}
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstError error
	slots := make(chan struct{}, 6)
	for i, resource := range resources {
		if resource.Type != "qemu" && resource.Type != "lxc" {
			continue
		}
		result[i] = AllocationVM{VMID: resource.VMID, Node: resource.Node}
		if offline[resource.Node] {
			continue
		}
		wg.Add(1)
		go func(i int, resource vmResource) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				if firstError == nil {
					firstError = allocationReadError{vmid: resource.VMID, node: resource.Node, cause: ctx.Err()}
				}
				mu.Unlock()
				return
			}
			defer func() { <-slots }()
			item := AllocationVM{VMID: resource.VMID, Node: resource.Node}
			path := "/api2/json/nodes/" + url.PathEscape(resource.Node) + "/" + resource.Type + "/" + strconv.Itoa(resource.VMID)
			configCtx, cancelConfig := context.WithTimeout(ctx, 5*time.Second)
			err := c.request(configCtx, http.MethodGet, path+"/config", nil, &item.Config)
			cancelConfig()
			if err != nil {
				if ctx.Err() == nil && unavailableAllocationNode(err) {
					return
				}
				mu.Lock()
				if firstError == nil {
					firstError = allocationReadError{vmid: resource.VMID, node: resource.Node, cause: err}
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

// Error messages identify the failed resource without exposing HTTP response
// bodies, authorization tokens or credential-bearing URLs.
type allocationReadError struct {
	vmid  int
	node  string
	cause error
}

func (e allocationReadError) Is(target error) bool {
	return e.vmid != 0 && target == ErrNetworkAllocationsUnavailable
}
func (e allocationReadError) Unwrap() error { return e.cause }
func (e allocationReadError) Error() string {
	target := "Proxmox VM allocations"
	if e.vmid != 0 {
		target = fmt.Sprintf("the network configuration of VM %d on node %q", e.vmid, e.node)
	}
	reason := "Check the Proxmox connection and node availability."
	var api apiError
	var dns *net.DNSError
	var timeout interface{ Timeout() bool }
	switch {
	case errors.As(e.cause, &api):
		switch api.Status {
		case http.StatusUnauthorized:
			reason = "Proxmox rejected the API token. Check its credentials."
		case http.StatusForbidden:
			reason = "The API token does not have permission to read this resource."
		case http.StatusNotFound:
			reason = "The resource is no longer available. Refresh the Proxmox inventory."
		default:
			if strings.Contains(strings.ToLower(api.Message), "hostname lookup") {
				reason = "Proxmox cannot resolve the node hostname. Check whether the node is online and its name is resolvable."
			} else {
				reason = fmt.Sprintf("Proxmox returned HTTP %d. Check the node availability.", api.Status)
			}
		}
	case errors.As(e.cause, &timeout) && timeout.Timeout():
		reason = "The request to Proxmox timed out."
	case errors.Is(e.cause, context.Canceled):
		reason = "The request was canceled."
	case errors.As(e.cause, &dns):
		reason = "The Proxmox address could not be resolved."
	}
	return fmt.Sprintf("Could not read %s. %s", target, reason)
}

func unavailableAllocationNode(err error) bool {
	var api apiError
	if errors.As(err, &api) {
		if api.Status == http.StatusBadGateway || api.Status == http.StatusServiceUnavailable || api.Status == http.StatusGatewayTimeout {
			return true
		}
		if api.Status == http.StatusInternalServerError {
			message := strings.ToLower(api.Message)
			for _, text := range []string{"hostname lookup", "connection refused", "no route to host", "failed to connect", "is offline", "is not online"} {
				if strings.Contains(message, text) {
					return true
				}
			}
		}
		return false
	}
	var network *net.OpError
	var timeout interface{ Timeout() bool }
	return errors.As(err, &network) || errors.As(err, &timeout) && timeout.Timeout()
}
