package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	proxmoxvm "kubephos.dev/kubephos/plugins/proxmox-vm/runtime"
)

func (s *Server) listProxmoxNodes(w http.ResponseWriter, r *http.Request) {
	connection, err := s.store.GetProviderConnectionConfiguration(r.Context(), r.PathValue("id"))
	if err != nil || connection.Provider != "proxmox" {
		writeError(w, 422, "invalid_connection", "Select an existing Proxmox connection.")
		return
	}
	var config map[string]any
	if json.Unmarshal(connection.Configuration, &config) != nil {
		writeError(w, 500, "invalid_connection", "Could not read the Proxmox connection.")
		return
	}
	credential, err := s.connectionCredential(r.Context(), config)
	if err != nil {
		writeError(w, 500, "credential_unavailable", "Could not read the Proxmox credential.")
		return
	}
	endpoint, _ := config["endpoint"].(string)
	verify, _ := config["verifyTLS"].(bool)
	tokenID, _ := credential["tokenId"].(string)
	tokenSecret, _ := credential["tokenSecret"].(string)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	nodes, err := proxmoxvm.ReadProxmoxNodes(ctx, endpoint, verify, tokenID, tokenSecret)
	if err != nil {
		writeError(w, 502, "nodes_unavailable", "Could not load Proxmox servers.")
		return
	}
	writeJSON(w, 200, map[string]any{"items": nodes})
}
