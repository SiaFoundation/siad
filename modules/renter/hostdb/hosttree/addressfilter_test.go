package hosttree

import (
	"net"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

var (
	ipv4Localhost = net.IP{127, 0, 0, 1}
	ipv6Localhost = net.IP{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
)

// testTooManyAddressesResolver is a resolver for the TestTwoAddresses test.
type testTooManyAddressesResolver struct{}

func (testTooManyAddressesResolver) lookupIP(host string) ([]net.IP, error) {
	return []net.IP{{}, {}, {}}, nil
}

// testTwoAddressesResolver is a resolver for the TestTwoAddresses test.
type testTwoAddressesResolver struct{}

func (testTwoAddressesResolver) lookupIP(host string) ([]net.IP, error) {
	switch host {
	case "ipv4.ipv6":
		return []net.IP{ipv4Localhost, ipv6Localhost}, nil
	case "ipv6.ipv4":
		return []net.IP{ipv6Localhost, ipv4Localhost}, nil
	case "ipv4.ipv4":
		return []net.IP{ipv4Localhost, ipv4Localhost}, nil
	case "ipv6.ipv6":
		return []net.IP{ipv6Localhost, ipv6Localhost}, nil
	default:
		panic("shouldn't happen")
	}
}

// testFilterIPv4Resolver is a resolver for the TestFilterIPv4 test.
type testFilterIPv4Resolver struct{}

func (testFilterIPv4Resolver) lookupIP(host string) ([]net.IP, error) {
	switch host {
	case "host1":
		return []net.IP{{127, 0, 0, 1}}, nil
	case "host2":
		return []net.IP{{127, 0, 0, 2}}, nil
	case "host3":
		return []net.IP{{127, 0, 1, 1}}, nil
	case "host4":
		return []net.IP{{127, 1, 1, 1}}, nil
	case "host5":
		return []net.IP{{128, 1, 1, 1}}, nil
	case "host6":
		return []net.IP{{128, 1, 1, 9}}, nil
	default:
		panic("shouldn't happen")
	}
}

// testFilterIPv6Resolver is a resolver for the TestFilterIPv6 test.
type testFilterIPv6Resolver struct{}

func (testFilterIPv6Resolver) lookupIP(host string) ([]net.IP, error) {
	switch host {
	case "host1":
		return []net.IP{{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host2":
		return []net.IP{{0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host3":
		return []net.IP{{0, 0, 0, 0, 0, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host4":
		return []net.IP{{0, 0, 0, 0, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host5":
		return []net.IP{{0, 0, 0, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host6":
		return []net.IP{{0, 0, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host7":
		return []net.IP{{0, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host8":
		return []net.IP{{1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1}}, nil
	case "host9":
		return []net.IP{{1, 1, 1, 1, 1, 1, 1, 0, 0, 1, 0, 0, 0, 0, 0, 1}}, nil
	default:
		panic("shouldn't happen")
	}
}

// newHostFromAddress is a convenience method to create a new hostEntry from an
// ip address.
func newHostFromAddress(address string) *hostEntry {
	host := &hostEntry{}
	host.NetAddress = modules.NetAddress(address + ":1234")
	return host
}

// TestTooManyAddresses checks that hosts with more than 2 associated IP
// addresses are always filtered.
func TestTooManyAddresses(t *testing.T) {
	// Check that returning more than 2 addresses causes a host to be filtered.
	filter := newProductionFilter(testTooManyAddressesResolver{})
	host := newHostFromAddress("any.address")

	// Add host to filter.
	filter.Add(host)

	// Host should be filtered.
	if !filter.Filtered(host) {
		t.Fatal("Host with too many addresses should be filtered but wasn't")
	}
}

// TestTwoAddresses checks that hosts with two addresses will be filtered if
// they have the same address type.
func TestTwoAddresses(t *testing.T) {
	// Check that returning more than 2 addresses causes a host to be filtered.
	filter := newProductionFilter(testTwoAddressesResolver{})

	// Create a few hosts for testing.
	hostValid1 := newHostFromAddress("ipv4.ipv6")
	hostValid2 := newHostFromAddress("ipv6.ipv4")
	hostInvalid1 := newHostFromAddress("ipv4.ipv4")
	hostInvalid2 := newHostFromAddress("ipv6.ipv6")

	// Check hosts.
	if filter.Filtered(hostValid1) || filter.Filtered(hostValid2) {
		t.Fatal("Valid hosts were filtered.")
	}
	if !filter.Filtered(hostInvalid1) || !filter.Filtered(hostInvalid2) {
		t.Fatal("Invalid hosts weren't filtered.")
	}
}

// TestFilterIPv4 tests filtering IPv4 addresses.
func TestFilterIPv4(t *testing.T) {
	filter := newProductionFilter(testFilterIPv4Resolver{})

	host1 := newHostFromAddress("host1")
	host2 := newHostFromAddress("host2")
	host3 := newHostFromAddress("host3")
	host4 := newHostFromAddress("host4")
	host5 := newHostFromAddress("host5")
	host6 := newHostFromAddress("host6")

	// Host1 shouldn't be filtered.
	if filter.Filtered(host1) {
		t.Error("host1 was filtered")
	}
	filter.Add(host1)

	// Host2 should be filtered.
	if !filter.Filtered(host2) {
		t.Error("host2 wasn't filtered")
	}

	// Host3 shouldn't be filtered.
	if filter.Filtered(host3) {
		t.Error("host3 was filtered")
	}
	filter.Add(host3)

	// Host4 shouldn't be filtered.
	if filter.Filtered(host4) {
		t.Error("host4 was filtered")
	}
	filter.Add(host4)

	// Host5 shouldn't be filtered.
	if filter.Filtered(host5) {
		t.Error("host5 was filtered")
	}
	filter.Add(host5)

	// Host6 should be filtered.
	if !filter.Filtered(host6) {
		t.Error("host6 wasn't filtered")
	}
}

// TestFilterIPv6 tests filtering IPv4 addresses.
func TestFilterIPv6(t *testing.T) {
	filter := newProductionFilter(testFilterIPv6Resolver{})

	host1 := newHostFromAddress("host1")
	host2 := newHostFromAddress("host2")
	host3 := newHostFromAddress("host3")
	host4 := newHostFromAddress("host4")
	host5 := newHostFromAddress("host5")
	host6 := newHostFromAddress("host6")
	host7 := newHostFromAddress("host7")
	host8 := newHostFromAddress("host8")
	host9 := newHostFromAddress("host9")

	// Host1 shouldn't be filtered.
	if filter.Filtered(host1) {
		t.Error("host1 was filtered")
	}
	filter.Add(host1)

	// Host2 should be filtered.
	if !filter.Filtered(host2) {
		t.Error("host2 wasn't filtered")
	}

	// Host3 shouldn't be filtered.
	if filter.Filtered(host3) {
		t.Error("host3 was filtered")
	}
	filter.Add(host3)

	// Host4 shouldn't be filtered.
	if filter.Filtered(host4) {
		t.Error("host4 was filtered")
	}
	filter.Add(host4)

	// Host5 shouldn't be filtered.
	if filter.Filtered(host5) {
		t.Error("host5 was filtered")
	}
	filter.Add(host5)

	// Host6 shouldn't be filtered.
	if filter.Filtered(host6) {
		t.Error("host6 was filtered")
	}
	filter.Add(host6)

	// Host7 shouldn't be filtered.
	if filter.Filtered(host7) {
		t.Error("host7 was filtered")
	}
	filter.Add(host7)

	// Host8 shouldn't be filtered.
	if filter.Filtered(host8) {
		t.Error("host8 was filtered")
	}
	filter.Add(host8)

	// Host9 should be filtered.
	if !filter.Filtered(host9) {
		t.Error("host9 wasn't filtered")
	}
}
