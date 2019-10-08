package siatest

import (
	"net"
	"testing"
)

// TestNextNodeAddress probes nextNodeAddress to verify that the addresses are
// indexing properly
func TestNextNodeAddress(t *testing.T) {
	if !testing.Short() {
		t.SkipNow()
	}
	// Confirm testNodeAddressCounter is initialized correctly
	ac := newNodeAddressCounter()
	if ac.address.String() != "127.1.0.0" {
		t.Fatalf("testNodeAddressCounter inital value incorrect; got %v expected %v", ac.address.String(), "127.1.0.0")
	}

	// Check address iteration
	nextIP, err := ac.managedNextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.1.0.1" {
		t.Fatalf("managedNextNodeAddress value incorrect; got %v expected %v", nextIP, "127.1.0.1")
	}

	// Test address iteration across range
	ac.address = net.ParseIP("127.0.0.255")
	nextIP, err = ac.managedNextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.0.1.0" {
		t.Fatalf("managedNextNodeAddress value incorrect; got %v expected %v", nextIP, "127.0.1.0")
	}

	// Test address iteration across multiple range
	ac.address = net.ParseIP("127.0.255.255")
	nextIP, err = ac.managedNextNodeAddress()
	if err != nil {
		t.Fatal(err)
	}
	if nextIP != "127.1.0.0" {
		t.Fatalf("managedNextNodeAddress value incorrect; got %v expected %v", nextIP, "127.1.0.0")
	}

	// Test last address iteration
	ac.address = net.ParseIP("127.255.255.255")
	nextIP, err = ac.managedNextNodeAddress()
	if err == nil {
		t.Fatal("Should have returned an error for reaching the last available address")
	}
}
