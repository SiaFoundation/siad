package addressfilter

import (
	"fmt"
	"net"
)

const (
	ipv4FilterRange = 24
	ipv6FilterRange = 54
)

// address is an interface that needs to be implemented by any type that should
// be compatible with a Filter.
type address interface {
	Host() string
}

// Resolver is an interface that allows resolving a hostname into IP
// addresses.
type Resolver interface {
	LookupIP(string) ([]net.IP, error)
}

// ProductionResolver is the hostname resolver used in production builds.
type ProductionResolver struct{}

// LookupIP is a passthrough function to net.LookupIP.
func (ProductionResolver) LookupIP(host string) ([]net.IP, error) {
	return net.LookupIP(host)
}

// Filter is the interface for a filter that can filter hostnames which
// share a certain IP mask.
type Filter interface {
	// Add adds a host to the filter. This will resolve the hostname into one
	// or more IP addresses, extract the subnets used by those addresses and
	// add the subnets to the filter. Add doesn't return an error, but if the
	// addresses of a host can't be resolved it will be handled as if the host
	// had no addresses associated with it.
	Add(address)

	// Filter checks if a host uses a subnet that is already in use by a host
	// that was previously added to the filter. If it is in use, or if the host
	// is associated with 2 addresses of the same type (e.g. IPv4 and IPv4) or
	// if it is associated with more than 2 addresses, Filtered will return
	// 'true'.
	Filtered(address) bool

	// Reset empties the filter.
	Reset()
}

// TestingFilter is the filter used during testing builds.
type TestingFilter struct{}

// Add is a no-op for the TestingFilter
func (TestingFilter) Add(address) {}

// Filtered is a no-op for the TestingFilter
func (TestingFilter) Filtered(address) bool { return false }

// Reset is a no-op for the TestingFilter
func (TestingFilter) Reset() {}

// ProductionFilter filters host addresses which belong to the same subnet to
// avoid selecting hosts from the same region.
type ProductionFilter struct {
	filter   map[string]struct{}
	resolver Resolver
}

// NewProductionFilter creates a new addressFilter object.
func NewProductionFilter(resolver Resolver) *ProductionFilter {
	return &ProductionFilter{
		filter:   make(map[string]struct{}),
		resolver: resolver,
	}
}

// Add adds the addresses from a host to the filter preventing addresses from
// the same subnets from being selected.
func (af *ProductionFilter) Add(host address) {
	// Translate the hostname to one or multiple IPs. If the argument is an IP
	// address LookupIP will just return that IP.
	addresses, err := af.resolver.LookupIP(host.Host())
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
func (af *ProductionFilter) Filtered(host address) bool {
	// Translate the hostname to one or multiple IPs. If the argument is an IP
	// address LookupIP will just return that IP.
	addresses, err := af.resolver.LookupIP(host.Host())
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
func (af *ProductionFilter) Reset() {
	af.filter = make(map[string]struct{})
}
