package provisioning

import (
	"strings"
	"testing"
)

func testNetwork(ip string) Network {
	return Network{Address: ip, PrefixLength: 24, Gateway: "10.20.0.1", DNSServer: "10.20.0.2", Bridge: "vmbr1"}
}
func TestSuggestionsIncludeReservationsAndConsecutiveRanges(t *testing.T) {
	inventory := NewInventory()
	inventory.Reserve(200, 3, testNetwork("10.20.0.20"))
	inventory.Reserve(350, 2, testNetwork("10.20.0.50"))
	suggestion, err := inventory.Suggest(5, Network{Bridge: "vmbr1"})
	if err != nil {
		t.Fatal(err)
	}
	if suggestion.VMID != 352 || suggestion.Address != "10.20.0.52" || suggestion.Gateway != "10.20.0.1" {
		t.Fatalf("unexpected suggestion %+v", suggestion)
	}
	if err := inventory.Validate(suggestion, 5); err != nil {
		t.Fatal(err)
	}
	// Manual choices are accepted when free, even below the proposed values.
	if err := inventory.Validate(Allocation{VMID: 100, Network: testNetwork("10.20.0.10")}, 2); err != nil {
		t.Fatal(err)
	}
}
func TestValidationRejectsConflictsAnywhereInBlock(t *testing.T) {
	inventory := NewInventory()
	inventory.Reserve(205, 1, testNetwork("10.20.0.25"))
	tests := []struct {
		name    string
		value   Allocation
		count   int
		message string
	}{
		{"vmid", Allocation{VMID: 200, Network: testNetwork("10.20.0.10")}, 6, "205"},
		{"ip", Allocation{VMID: 300, Network: testNetwork("10.20.0.20")}, 6, "10.20.0.25"},
		{"gateway", Allocation{VMID: 300, Network: testNetwork("10.20.0.1")}, 1, "gateway"},
		{"broadcast", Allocation{VMID: 300, Network: testNetwork("10.20.0.254")}, 2, "subnet"},
		{"invalid", Allocation{VMID: 300, Network: testNetwork("bad")}, 1, "IPv4"},
		{"vmid overflow", Allocation{VMID: 999999999}, 2, "range"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := inventory.Validate(test.value, test.count)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("got %v", err)
			}
		})
	}
}
func TestSuggestionUsesSubnetHolesAndTemplateBridge(t *testing.T) {
	inventory := NewInventory()
	other := testNetwork("10.20.0.250")
	other.Bridge = "vmbr0"
	inventory.AddNetwork(other)
	selected := testNetwork("192.168.5.254")
	selected.Gateway = "192.168.5.1"
	inventory.AddNetwork(selected)
	value, err := inventory.Suggest(4, Network{Bridge: "vmbr1"})
	if err != nil {
		t.Fatal(err)
	}
	if value.Address != "192.168.5.2" {
		t.Fatalf("wrong network or unavailable block %+v", value)
	}
}
func TestConfigNetworksReadsMultipleInterfacesAndLXC(t *testing.T) {
	networks := ConfigNetworks(map[string]any{"ipconfig0": "ip=10.20.0.4/24,gw=10.20.0.1", "net0": "virtio=AB,tag=42,bridge=vmbr0", "ipconfig1": "ip=192.168.1.2/25", "net1": "virtio=CD,bridge=vmbr1", "nameserver": "10.20.0.2 1.1.1.1"})
	if len(networks) != 2 || networks[0].Bridge != "vmbr0/42" || networks[1].PrefixLength != 25 || networks[0].DNSServer != "10.20.0.2" {
		t.Fatalf("%+v", networks)
	}
	networks = ConfigNetworks(map[string]any{"net0": "name=eth0,bridge=vmbr1,ip=10.20.0.3/24,gw=10.20.0.1", "net1": "name=eth1,ip=dhcp"})
	if len(networks) != 1 || networks[0].Address != "10.20.0.3" {
		t.Fatalf("%+v", networks)
	}
}
func TestNoNetworkIsInvented(t *testing.T) {
	value, err := NewInventory().Suggest(1, Network{})
	if err != nil || value.VMID != 100 || value.Address != "" {
		t.Fatalf("%+v %v", value, err)
	}
}
