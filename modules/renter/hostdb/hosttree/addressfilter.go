package hosttree

import (
	"fmt"
	"net"
)

const (
	ipv4FilterRange = 24
	ipv6FilterRange = 54
)

// hostResolver is an interface that allows resolving a hostname into IP
// addresses.
type hostResolver interface {
	lookupIP(string) ([]net.IP, error)
}

// productionResolver is the hostname resolver used in production builds.
type productionResolver struct{}

func (productionResolver) lookupIP(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// addressFilter is the interface for a filter that can filter hostnames which
// share a certain IP mask.
type addressFilter interface {
	// Add adds a host to the filter. This will resolve the hostname into one
	// or more IP addresses, extract the subnets used by those addresses and
	// add the subnets to the filter. Add doesn't return an error, but if the
	// addresses of a host can't be resolved it will be handled as if the host
	// had no addresses associated with it.
	Add(*hostEntry)

	// Filter checks if a host uses a subnet that is already in use by a host
	// that was previously added to the filter. If it is in use, or if the host
	// is associated with 2 addresses of the same type (e.g. IPv4 and IPv4) or
	// if it is associated with more than 2 addresses, Filtered will return
	// 'true'.
	Filtered(*hostEntry) bool

	// Reset empties the filter.
	Reset()
}

// testingFilter is the filter used during testing builds.
type testingFilter struct{}

func (testingFilter) Add(*hostEntry)           {}
func (testingFilter) Filtered(*hostEntry) bool { return false }
func (testingFilter) Reset()                   {}

// productionFilter filters host addresses which belong to the same subnet to
// avoid selecting hosts from the same region.
type productionFilter struct {
	filter   map[string]struct{}
	resolver hostResolver
}

// newProductionFilter creates a new addressFilter object.
func newProductionFilter(resolver hostResolver) *productionFilter {
	return &productionFilter{
		filter:   make(map[string]struct{}),
		resolver: resolver,
	}
}

// Add adds the addresses from a host to the filter preventing addresses from
// the same subnets from being selected.
func (af *productionFilter) Add(host *hostEntry) {
	// Translate the hostname to one or multiple IPs. If the argument is an IP
	// address LookupIP will just return that IP.
	addresses, err := af.resolver.lookupIP(host.NetAddress.Host())
	if err != nil {
		return
	}
	// If any of the addresses is blocked we ignore the host.
	for _, ip := range addresses {
		// Set the filterRange according to the type of IP address.
		var filterRange int
		if ip.To4() != nil {
			filterRange = ipv4FilterRange
		} else {
			filterRange = ipv6FilterRange
		}
		// Get the subnet.
		_, ipnet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), filterRange))
		if err != nil {
			continue
		}
		// Add the subnet to the map.
		af.filter[ipnet.String()] = struct{}{}
	}
}

// Filtered returns true if an address is supposed to be filtered and therefore
// not selected by the hosttree.
func (af *productionFilter) Filtered(host *hostEntry) bool {
	// Translate the hostname to one or multiple IPs. If the argument is an IP
	// address LookupIP will just return that IP.
	addresses, err := af.resolver.lookupIP(host.NetAddress.Host())
	if err != nil {
		return true
	}
	// If the hostname is associated with more than 2 addresses we filter it
	if len(addresses) > 2 {
		return true
	}
	// If the hostname is associated with 2 addresses of the same type, we
	// filter it.
	if (len(addresses) == 2) && (len(addresses[0]) == len(addresses[1])) {
		return true
	}
	// If any of the addresses is blocked we ignore the host.
	for _, ip := range addresses {
		// Set the filterRange according to the type of IP address.
		var filterRange int
		if ip.To4() != nil {
			filterRange = ipv4FilterRange
		} else {
			filterRange = ipv6FilterRange
		}
		// Get the subnet.
		_, ipnet, err := net.ParseCIDR(fmt.Sprintf("%s/%d", ip.String(), filterRange))
		if err != nil {
			continue
		}
		// Check if the subnet is in the map. If it is, we filter the host.
		if _, exists := af.filter[ipnet.String()]; exists {
			return true
		}
	}
	return false
}

// Reset clears the filter's contents.
func (af *productionFilter) Reset() {
	af.filter = make(map[string]struct{})
}
