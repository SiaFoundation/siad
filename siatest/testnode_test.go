package siatest

import (
	"net"
	"testing"
)

// TestNextNodeAddress probes nextNodeAddress to verify that the addresses are
// indexing properly
func TestNextNodeAddress(t *testing.T) {
	// Confirm testNodeAddressCounter is initialized correctly
	IP := <-testNodeAddressCounter
	if IP.String() != "127.0.1.0" {
		t.Fatalf("testNodeAddressCounter inital value incorrect; got %v expected %v", IP.String(), "127.0.1.0")
	}

	// Send IP back to channel
	testNodeAddressCounter <- IP

	// Check address iteration
	nextIP, err := nextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.0.1.1" {
		t.Fatalf("nextNodeAddress value incorrect; got %v expected %v", nextIP, "127.0.1.1")
	}

	// Test address iteration across range
	IP = <-testNodeAddressCounter
	IP = net.ParseIP("127.0.0.255")
	testNodeAddressCounter <- IP
	nextIP, err = nextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.0.1.0" {
		t.Fatalf("nextNodeAddress value incorrect; got %v expected %v", nextIP, "127.0.1.0")
	}

	// Test address iteration across multiple range
	IP = <-testNodeAddressCounter
	IP = net.ParseIP("127.0.255.255")
	testNodeAddressCounter <- IP
	nextIP, err = nextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.1.0.0" {
		t.Fatalf("nextNodeAddress value incorrect; got %v expected %v", nextIP, "127.1.0.0")
	}

	// Test last address iteration
	IP = <-testNodeAddressCounter
	IP = net.ParseIP("127.255.255.255")
	testNodeAddressCounter <- IP
	nextIP, err = nextNodeAddress()
	if err == nil {
		t.Fatal("Should have returned an error for reaching the last available address")
	}
}
