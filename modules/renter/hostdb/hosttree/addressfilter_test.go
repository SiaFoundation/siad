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

// host is a implementation of the address interface for testing.
type host struct {
	address string
}

// Host returns the address field of the host struct.
func (h host) Host() string {
	return h.address
}

// testTooManyAddressesResolver is a resolver for the TestTwoAddresses test.
type testTooManyAddressesResolver struct{}

func (testTooManyAddressesResolver) LookupIP(host string) ([]net.IP, error) {
	return []net.IP{{}, {}, {}}, nil
}

// testTwoAddressesResolver is a resolver for the TestTwoAddresses test.
type testTwoAddressesResolver struct{}

func (testTwoAddressesResolver) LookupIP(host string) ([]net.IP, error) {
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

func (testFilterIPv4Resolver) LookupIP(host string) ([]net.IP, error) {
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

func (testFilterIPv6Resolver) LookupIP(host string) ([]net.IP, error) {
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

// TestTooManyAddresses checks that hosts with more than 2 associated IP
// addresses are always filtered.
func TestTooManyAddresses(t *testing.T) {
	// Check that returning more than 2 addresses causes a host to be filtered.
	filter := NewFilter(testTooManyAddressesResolver{})
	host := modules.NetAddress("any.address:1234")

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
	filter := NewFilter(testTwoAddressesResolver{})

	// Create a few hosts for testing.
	hostValid1 := modules.NetAddress("ipv4.ipv6:1234")
	hostValid2 := modules.NetAddress("ipv6.ipv4:1234")
	hostInvalid1 := modules.NetAddress("ipv4.ipv4:1234")
	hostInvalid2 := modules.NetAddress("ipv6.ipv6:1234")

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
	filter := NewFilter(testFilterIPv4Resolver{})

	host1 := modules.NetAddress("host1:1234")
	host2 := modules.NetAddress("host2:1234")
	host3 := modules.NetAddress("host3:1234")
	host4 := modules.NetAddress("host4:1234")
	host5 := modules.NetAddress("host5:1234")
	host6 := modules.NetAddress("host6:1234")

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
	filter := NewFilter(testFilterIPv6Resolver{})

	host1 := modules.NetAddress("host1:1234")
	host2 := modules.NetAddress("host2:1234")
	host3 := modules.NetAddress("host3:1234")
	host4 := modules.NetAddress("host4:1234")
	host5 := modules.NetAddress("host5:1234")
	host6 := modules.NetAddress("host6:1234")
	host7 := modules.NetAddress("host7:1234")
	host8 := modules.NetAddress("host8:1234")
	host9 := modules.NetAddress("host9:1234")

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
