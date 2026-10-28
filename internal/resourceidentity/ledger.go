// Package resourceidentity separates provider-local resource identifiers from
// ledger identities. Plugin artifacts retain their original provider-local IDs.
package resourceidentity

import (
	"encoding/json"
	"errors"
	"regexp"
)

var connectionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// LedgerID scopes Proxmox resources to the selected connection, never to a node:
// VMIDs are unique across all nodes of one Proxmox cluster.
func LedgerID(provider string, input json.RawMessage, externalID string) (string, error) {
	if provider != "proxmox" {
		return externalID, nil
	}
	var spec struct {
		ConnectionRef string `json:"connectionRef"`
	}
	if json.Unmarshal(input, &spec) != nil || !connectionIDPattern.MatchString(spec.ConnectionRef) || externalID == "" {
		return "", errors.New("Proxmox resource identity requires a valid connectionRef")
	}
	return spec.ConnectionRef + "/" + externalID, nil
}
