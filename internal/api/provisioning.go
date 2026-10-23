package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/provisioning"
	"kubephos.dev/kubephos/internal/storage"
	proxmoxvm "kubephos.dev/kubephos/plugins/proxmox-vm/runtime"
)

func (s *Server) provisioningInventory(ctx context.Context, connectionID, templateID string) (*provisioning.Inventory, provisioning.Network, error) {
	connection, err := s.store.GetProviderConnectionConfiguration(ctx, connectionID)
	if err != nil {
		return nil, provisioning.Network{}, err
	}
	if connection.Provider != "proxmox" {
		return nil, provisioning.Network{}, fmt.Errorf("Select a Proxmox connection.")
	}
	var config map[string]any
	if err = json.Unmarshal(connection.Configuration, &config); err != nil {
		return nil, provisioning.Network{}, err
	}
	credential, err := s.connectionCredential(ctx, config)
	if err != nil {
		return nil, provisioning.Network{}, err
	}
	endpoint, _ := config["endpoint"].(string)
	verifyTLS, _ := config["verifyTLS"].(bool)
	tokenID, _ := credential["tokenId"].(string)
	tokenSecret, _ := credential["tokenSecret"].(string)
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	vms, err := proxmoxvm.ReadAllocationInventory(ctx, endpoint, verifyTLS, tokenID, tokenSecret)
	if err != nil {
		return nil, provisioning.Network{}, fmt.Errorf("Could not read Proxmox VM and network allocations.")
	}
	inventory := provisioning.NewInventory()
	preferred := provisioning.Network{}
	templateVMID := 0
	if templateID != "" {
		template, err := s.store.GetManagedResource(ctx, templateID)
		if err != nil || template.Kind != "machine-template" || template.ConnectionID != connectionID {
			return nil, preferred, fmt.Errorf("Select a template on the same Proxmox connection.")
		}
		var spec map[string]any
		json.Unmarshal(template.Spec, &spec)
		preferred = provisioning.NetworkFromSpec(spec)
		templateVMID = intField(spec, "vmid")
	}
	for _, vm := range vms {
		inventory.VMIDs[vm.VMID] = true
		networks := provisioning.ConfigNetworks(vm.Config)
		for _, network := range networks {
			inventory.AddNetwork(network)
		}
		if vm.VMID == templateVMID {
			if len(networks) > 0 {
				preferred = networks[0]
			}
			if preferred.Bridge == "" {
				if net0, ok := vm.Config["net0"].(string); ok {
					preferred.Bridge = networkBridge(net0)
				}
			}
		}
		for _, address := range vm.Addresses {
			inventory.AddNetwork(provisioning.Network{Address: address})
		}
	}
	claims, err := s.store.ProvisioningClaims(ctx, connectionID)
	if err != nil {
		return nil, preferred, err
	}
	// Put managed network profiles first; they reflect the network selected in previous forms.
	liveProfiles := inventory.Networks
	inventory.Networks = nil
	for _, claim := range claims {
		var spec map[string]any
		if json.Unmarshal(claim.Spec, &spec) != nil {
			return nil, preferred, fmt.Errorf("Could not read saved VM allocations.")
		}
		for _, spec := range provisioningClaimSpecs(spec) {
			count, vmid := 1, intField(spec, "vmid")
			if claim.Kind == "kubernetes-cluster" {
				vmid = intField(spec, "baseVMID")
				count = intField(spec, "controlPlanes")
				if pool, ok := spec["managementPool"].(map[string]any); ok {
					count += intField(pool, "count")
				}
				if pools, ok := spec["applicationPools"].([]any); ok {
					for _, pool := range pools {
						if item, ok := pool.(map[string]any); ok {
							count += intField(item, "count")
						}
					}
				}
			}
			if vmid >= 100 && count > 0 && count <= 108 {
				inventory.Reserve(vmid, count, provisioning.NetworkFromSpec(spec))
			}
		}
	}
	inventory.Networks = append(inventory.Networks, liveProfiles...)
	return inventory, preferred, nil
}
func networkBridge(value string) string {
	var bridge, tag string
	for _, part := range strings.Split(value, ",") {
		if len(part) > 7 && part[:7] == "bridge=" {
			bridge = part[7:]
		}
		if len(part) > 4 && part[:4] == "tag=" {
			tag = part[4:]
		}
	}
	if tag != "" {
		bridge += "/" + tag
	}
	return bridge
}
func intField(spec map[string]any, key string) int {
	value, _ := strconv.Atoi(fmt.Sprint(spec[key]))
	return value
}

func (s *Server) getProvisioningSuggestions(response http.ResponseWriter, request *http.Request) {
	count := 1
	if raw := request.URL.Query().Get("count"); raw != "" {
		var err error
		count, err = strconv.Atoi(raw)
		if err != nil || count < 1 || count > 108 {
			writeError(response, http.StatusUnprocessableEntity, "invalid_count", "Select a valid machine count.")
			return
		}
	}
	inventory, preferred, err := s.provisioningInventory(request.Context(), request.PathValue("id"), request.URL.Query().Get("templateId"))
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "inventory_unavailable", err.Error())
		return
	}
	value, err := inventory.Suggest(count, preferred)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "allocation_unavailable", err.Error())
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	writeJSON(response, http.StatusOK, value)
}

// validateProvisioning keeps the lock until the caller has persisted the queued resource.
func (s *Server) validateProvisioning(response http.ResponseWriter, request *http.Request, connectionID string, spec map[string]any, count int) (func(), bool) {
	release, err := s.store.LockProvisioning(request.Context(), connectionID)
	if err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(response, http.StatusConflict, "allocation_busy", "Another provisioning request is being validated. Please retry.")
		} else {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reserve VM allocations.")
		}
		return nil, false
	}
	inventory, _, err := s.provisioningInventory(request.Context(), connectionID, "")
	if err == nil {
		vmid := intField(spec, "vmid")
		if vmid == 0 {
			vmid = intField(spec, "baseVMID")
		}
		err = inventory.Validate(provisioning.Allocation{VMID: vmid, Network: provisioning.NetworkFromSpec(spec)}, count)
	}
	if err != nil {
		release()
		writeError(response, http.StatusUnprocessableEntity, "allocation_conflict", err.Error())
		return nil, false
	}
	return release, true
}

func provisioningClaimSpecs(spec map[string]any) []map[string]any {
	if spec["mode"] != "multi" {
		return []map[string]any{spec}
	}
	result := []map[string]any{}
	if members, ok := spec["members"].([]any); ok {
		for _, member := range members {
			if value, ok := member.(map[string]any); ok {
				result = append(result, value)
			}
		}
	}
	return result
}
