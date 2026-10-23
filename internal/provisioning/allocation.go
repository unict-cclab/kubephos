// Package provisioning checks and proposes consecutive VM and IPv4 allocations.
package provisioning

import (
	"encoding/binary"
	"fmt"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type Network struct {
	Address      string `json:"address,omitempty"`
	PrefixLength int    `json:"prefixLength,omitempty"`
	Gateway      string `json:"gateway,omitempty"`
	DNSServer    string `json:"dnsServer,omitempty"`
	Bridge       string `json:"-"`
}
type Inventory struct {
	VMIDs     map[int]bool
	Addresses map[string]bool
	Networks  []Network
}
type Allocation struct {
	VMID int `json:"vmid"`
	Network
}

func NewInventory() *Inventory {
	return &Inventory{VMIDs: map[int]bool{}, Addresses: map[string]bool{}}
}
func (inventory *Inventory) AddNetwork(network Network) {
	if address, err := netip.ParseAddr(network.Address); err == nil && address.Is4() {
		inventory.Addresses[address.String()] = true
		if network.PrefixLength >= 8 && network.PrefixLength <= 30 {
			inventory.Networks = append(inventory.Networks, network)
		}
	}
}
func number(address netip.Addr) uint32 { v := address.As4(); return binary.BigEndian.Uint32(v[:]) }
func address(value uint32) string {
	var bytes [4]byte
	binary.BigEndian.PutUint32(bytes[:], value)
	return netip.AddrFrom4(bytes).String()
}
func (inventory *Inventory) Validate(value Allocation, count int) error {
	if count < 1 || count > 108 || value.VMID < 100 || value.VMID > 999999999-count+1 {
		return fmt.Errorf("Select a valid VM ID range.")
	}
	for id := value.VMID; id < value.VMID+count; id++ {
		if inventory.VMIDs[id] {
			return fmt.Errorf("VM ID %d is already in use or reserved.", id)
		}
	}
	if value.Address == "" {
		return nil
	} // VM templates may use DHCP.
	ip, err := netip.ParseAddr(value.Address)
	if err != nil || !ip.Is4() || value.PrefixLength < 8 || value.PrefixLength > 30 {
		return fmt.Errorf("Use an IPv4 address and a prefix between 8 and 30.")
	}
	subnet := netip.PrefixFrom(ip, value.PrefixLength).Masked()
	first := uint64(number(ip))
	network := uint64(number(subnet.Addr()))
	broadcast := network + (1 << uint(32-value.PrefixLength)) - 1
	if first <= network || first+uint64(count)-1 >= broadcast {
		return fmt.Errorf("The IP range must fit within the subnet, excluding network and broadcast addresses.")
	}
	gateway, err := netip.ParseAddr(value.Gateway)
	if err != nil || !gateway.Is4() || !subnet.Contains(gateway) || uint64(number(gateway)) == network || uint64(number(gateway)) == broadcast {
		return fmt.Errorf("Gateway must be a host address in the same subnet.")
	}
	for i := 0; i < count; i++ {
		ip := address(uint32(first) + uint32(i))
		if ip == gateway.String() {
			return fmt.Errorf("The IP range includes the gateway %s.", ip)
		}
		if inventory.Addresses[ip] {
			return fmt.Errorf("IP address %s is already in use or reserved.", ip)
		}
	}
	if dns, err := netip.ParseAddr(value.DNSServer); err != nil || !dns.Is4() {
		return fmt.Errorf("Use a valid IPv4 DNS server.")
	}
	return nil
}
func (inventory *Inventory) Suggest(count int, preferred Network) (Allocation, error) {
	if count < 1 || count > 108 {
		return Allocation{}, fmt.Errorf("Invalid machine count.")
	}
	result := Allocation{VMID: 100}
	for id := range inventory.VMIDs {
		if id >= result.VMID {
			result.VMID = id + 1
		}
	}
	// Reuse gaps if the highest VM ID leaves no room.
	if result.VMID > 999999999-count+1 {
		result.VMID = 100
		for result.VMID <= 999999999-count+1 {
			free := true
			for i := 0; i < count; i++ {
				if inventory.VMIDs[result.VMID+i] {
					result.VMID += i + 1
					free = false
					break
				}
			}
			if free {
				break
			}
		}
	}
	candidates := append([]Network{preferred}, inventory.Networks...)
	for _, profile := range candidates {
		ip, err := netip.ParseAddr(profile.Address)
		if err != nil || !ip.Is4() || profile.PrefixLength < 8 || profile.PrefixLength > 30 {
			continue
		}
		if profile.Gateway == "" || profile.DNSServer == "" {
			continue
		}
		if preferred.Bridge != "" && profile.Bridge != "" && profile.Bridge != preferred.Bridge {
			continue
		}
		if NewInventory().Validate(Allocation{VMID: result.VMID, Network: profile}, 1) != nil {
			continue
		}
		subnet := netip.PrefixFrom(ip, profile.PrefixLength).Masked()
		start := number(ip) + 1
		for used := range inventory.Addresses {
			a, err := netip.ParseAddr(used)
			if err == nil && a.Is4() && subnet.Contains(a) && number(a) >= start {
				start = number(a) + 1
			}
		}
		end := uint64(number(subnet.Addr())) + (1 << uint(32-profile.PrefixLength)) - 1
		// First try after the last assigned address, then search holes in the subnet.
		starts := []uint32{start, number(subnet.Addr()) + 1}
		for _, start := range starts {
			for candidate := uint64(start); candidate+uint64(count) <= end; candidate++ {
				profile.Address = address(uint32(candidate))
				value := Allocation{VMID: result.VMID, Network: profile}
				if inventory.Validate(value, count) == nil {
					return value, nil
				}
			}
		}
	}
	return result, nil
}

// ConfigNetworks extracts every static interface, including LXC netN and QEMU ipconfigN.
func ConfigNetworks(config map[string]any) []Network {
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []Network{}
	for _, key := range keys {
		if !strings.HasPrefix(key, "ipconfig") && !strings.HasPrefix(key, "net") {
			continue
		}
		text, _ := config[key].(string)
		fields := map[string]string{}
		for _, part := range strings.Split(text, ",") {
			pair := strings.SplitN(part, "=", 2)
			if len(pair) == 2 {
				fields[pair[0]] = pair[1]
			}
		}
		prefix, err := netip.ParsePrefix(fields["ip"])
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		bridge := fields["bridge"] + "/" + fields["tag"]
		if strings.HasPrefix(key, "ipconfig") {
			netText, _ := config["net"+strings.TrimPrefix(key, "ipconfig")].(string)
			var name, tag string
			for _, part := range strings.Split(netText, ",") {
				if strings.HasPrefix(part, "bridge=") {
					name = strings.TrimPrefix(part, "bridge=")
				}
				if strings.HasPrefix(part, "tag=") {
					tag = strings.TrimPrefix(part, "tag=")
				}
			}
			bridge = name
			if tag != "" {
				bridge += "/" + tag
			}
		}
		dns, _ := config["nameserver"].(string)
		dnsFields := strings.Fields(strings.ReplaceAll(dns, ",", " "))
		if len(dnsFields) > 0 {
			dns = dnsFields[0]
		}
		result = append(result, Network{Address: prefix.Addr().String(), PrefixLength: prefix.Bits(), Gateway: fields["gw"], DNSServer: dns, Bridge: strings.TrimSuffix(bridge, "/")})
	}
	return result
}
func NetworkFromSpec(spec map[string]any) Network {
	prefix, _ := strconv.Atoi(fmt.Sprint(spec["prefixLength"]))
	value := func(key string) string { text, _ := spec[key].(string); return text }
	ip := value("address")
	if ip == "" {
		ip = value("addressStart")
	}
	return Network{Address: ip, PrefixLength: prefix, Gateway: value("gateway"), DNSServer: value("dnsServer"), Bridge: value("bridge")}
}
func (inventory *Inventory) Reserve(vmid, count int, network Network) {
	ip, err := netip.ParseAddr(network.Address)
	for i := 0; i < count; i++ {
		inventory.VMIDs[vmid+i] = true
		if err == nil && ip.Is4() {
			next := network
			next.Address = address(number(ip) + uint32(i))
			inventory.AddNetwork(next)
		}
	}
}
