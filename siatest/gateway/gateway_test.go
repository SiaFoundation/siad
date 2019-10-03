package gateway

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestGatewayRatelimit makes sure that we can set the gateway's ratelimits
// using the API and that they are persisted correctly.
func TestGatewayRatelimit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := gatewayTestDir(t.Name())

	// Create a new server
	testNode, err := siatest.NewCleanNode(node.Gateway(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Get the current ratelimits.
	gg, err := testNode.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	// Speeds should be 0 which means it's not being rate limited.
	if gg.MaxDownloadSpeed != 0 || gg.MaxUploadSpeed != 0 {
		t.Fatalf("Limits should be 0 but were %v and %v", gg.MaxDownloadSpeed, gg.MaxUploadSpeed)
	}
	// Change the limits.
	ds := int64(100)
	us := int64(200)
	if err := testNode.GatewayRateLimitPost(ds, us); err != nil {
		t.Fatal(err)
	}
	// Get the ratelimit again.
	gg, err = testNode.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	// Limit should be set correctly.
	if gg.MaxDownloadSpeed != ds || gg.MaxUploadSpeed != us {
		t.Fatalf("Limits should be %v/%v but are %v/%v",
			ds, us, gg.MaxDownloadSpeed, gg.MaxUploadSpeed)
	}
	// Restart the node.
	if err := testNode.RestartNode(); err != nil {
		t.Fatal(err)
	}
	// Get the ratelimit again.
	gg, err = testNode.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	// Limit should've been persisted correctly.
	if gg.MaxDownloadSpeed != ds || gg.MaxUploadSpeed != us {
		t.Fatalf("Limits should be %v/%v but are %v/%v",
			ds, us, gg.MaxDownloadSpeed, gg.MaxUploadSpeed)
	}
}

// TestGatewayBlacklist probes the gateway blacklist endpoints
func TestGatewayBlacklist(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Gateway
	testDir := gatewayTestDir(t.Name())
	gateway, err := siatest.NewCleanNode(node.Gateway(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gateway.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get current blacklist, should be empty
	blacklist, err := gateway.GatewayBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blacklist.Blacklist) != 0 {
		t.Fatalf("Expected blacklist to be empty, got %v", blacklist)
	}

	// Set the Gateways's blacklist
	addr1 := modules.NetAddress("123.123.123.123:0")
	addr2 := modules.NetAddress("456.456.456.456:0")
	addresses := []modules.NetAddress{addr1, addr2}
	err = gateway.GatewaySetBlacklistPost(addresses)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they are on the blacklist
	blacklist, err = gateway.GatewayBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blacklist.Blacklist) != len(addresses) {
		t.Fatalf("Expected blacklist to be %v, got %v", len(addresses), blacklist)
	}
	blacklistMap := make(map[string]struct{})
	blacklistMap[blacklist.Blacklist[0]] = struct{}{}
	blacklistMap[blacklist.Blacklist[1]] = struct{}{}
	for _, addr := range addresses {
		if _, ok := blacklistMap[addr.Host()]; !ok {
			t.Fatalf("Did not find %v in the blacklist", addr.Host())
		}
	}

	// Append an address to the gateway
	addr3 := modules.NetAddress("789.789.789.789:0")
	err = gateway.GatewayAppendBlacklistPost([]modules.NetAddress{})
	if err == nil {
		t.Fatal("Should return an error if trying to append no addresses")
	}
	err = gateway.GatewayAppendBlacklistPost([]modules.NetAddress{addr3})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they are on the blacklist
	blacklist, err = gateway.GatewayBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blacklist.Blacklist) != len(addresses)+1 {
		t.Fatalf("Expected blacklist to be %v, got %v", len(addresses)+1, blacklist)
	}
	blacklistMap = make(map[string]struct{})
	blacklistMap[blacklist.Blacklist[0]] = struct{}{}
	blacklistMap[blacklist.Blacklist[1]] = struct{}{}
	blacklistMap[blacklist.Blacklist[2]] = struct{}{}
	if _, ok := blacklistMap[addr3.Host()]; !ok {
		t.Fatalf("Address %v not found in blacklist %v", addr3.Host(), blacklist)
	}

	// Remove some from the blacklist
	err = gateway.GatewayRemoveBlacklistPost([]modules.NetAddress{})
	if err == nil {
		t.Fatal("Should be an error if submitting remove without a list of addresses")
	}
	err = gateway.GatewayRemoveBlacklistPost([]modules.NetAddress{modules.NetAddress(addr1)})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they were removed
	blacklist, err = gateway.GatewayBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blacklist.Blacklist) != len(addresses) {
		t.Fatalf("Expected blacklist to be %v, got %v", len(addresses), blacklist)
	}
	for _, addr := range blacklist.Blacklist {
		if addr == addr1.Host() {
			t.Fatalf("Found %v in the blacklist even though it should have been removed", addr1.Host())
		}
	}

	// Reset the blacklist
	err = gateway.GatewaySetBlacklistPost([]modules.NetAddress{})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the blacklist is empty
	blacklist, err = gateway.GatewayBlacklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blacklist.Blacklist) != 0 {
		t.Fatalf("Expected blacklist to be empty, got %v", blacklist)
	}
}
