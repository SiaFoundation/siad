package gateway

import (
	"errors"
	"testing"
	"time"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/siatest/dependencies"
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

// TestGatewayBlocklist probes the gateway blocklist endpoints
func TestGatewayBlocklist(t *testing.T) {
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

	// Get current blocklist, should be empty
	blocklist, err := gateway.GatewayBlocklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocklist.Blocklist) != 0 {
		t.Fatalf("Expected blocklist to be empty, got %v", blocklist)
	}

	// Set the Gateways's blocklist
	addr1 := "123.123.123.123"
	addr2 := "456.456.456.456"
	addresses := []string{addr1, addr2}
	err = gateway.GatewaySetBlocklistPost(addresses)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they are on the blocklist
	blocklist, err = gateway.GatewayBlocklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocklist.Blocklist) != len(addresses) {
		t.Fatalf("Expected blocklist to be %v, got %v", len(addresses), blocklist)
	}
	blocklistMap := make(map[string]struct{})
	blocklistMap[blocklist.Blocklist[0]] = struct{}{}
	blocklistMap[blocklist.Blocklist[1]] = struct{}{}
	for _, addr := range addresses {
		if _, ok := blocklistMap[addr]; !ok {
			t.Fatalf("Did not find %v in the blocklist", addr)
		}
	}

	// Append an address to the gateway
	addr3 := "789.789.789.789"
	err = gateway.GatewayAppendBlocklistPost([]string{})
	if err == nil {
		t.Fatal("Should return an error if trying to append no addresses")
	}
	err = gateway.GatewayAppendBlocklistPost([]string{addr3})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they are on the blocklist
	blocklist, err = gateway.GatewayBlocklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocklist.Blocklist) != len(addresses)+1 {
		t.Fatalf("Expected blocklist to be %v, got %v", len(addresses)+1, blocklist)
	}
	blocklistMap = make(map[string]struct{})
	blocklistMap[blocklist.Blocklist[0]] = struct{}{}
	blocklistMap[blocklist.Blocklist[1]] = struct{}{}
	blocklistMap[blocklist.Blocklist[2]] = struct{}{}
	if _, ok := blocklistMap[addr3]; !ok {
		t.Fatalf("Address %v not found in blocklist %v", addr3, blocklist)
	}

	// Remove some from the blocklist
	err = gateway.GatewayRemoveBlocklistPost([]string{})
	if err == nil {
		t.Fatal("Should be an error if submitting remove without a list of addresses")
	}
	err = gateway.GatewayRemoveBlocklistPost([]string{addr1})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm they were removed
	blocklist, err = gateway.GatewayBlocklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocklist.Blocklist) != len(addresses) {
		t.Fatalf("Expected blocklist to be %v, got %v", len(addresses), blocklist)
	}
	for _, addr := range blocklist.Blocklist {
		if addr == addr1 {
			t.Fatalf("Found %v in the blocklist even though it should have been removed", addr1)
		}
	}

	// Reset the blocklist
	err = gateway.GatewaySetBlocklistPost([]string{})
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the blocklist is empty
	blocklist, err = gateway.GatewayBlocklistGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(blocklist.Blocklist) != 0 {
		t.Fatalf("Expected blocklist to be empty, got %v", blocklist)
	}
}

// TestGatewayOfflineAlert tests if a gateway correctly registers the
// appropriate alert when it is online.
func TestGatewayOfflineAlert(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := gatewayTestDir(t.Name())

	// Create a new server
	params := node.Gateway(testDir)
	params.GatewayDeps = &dependencies.DependencyDisableAutoOnline{}
	testNode, err := siatest.NewCleanNode(params)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// Create a second server to connect to.
	testNode2, err := siatest.NewCleanNodeAsync(node.Gateway(testDir + "2"))
	if err != nil {
		t.Fatal(err)
	}
	// Test that gateway registered alert.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, err := testNode.GatewayGet()
		if err != nil {
			t.Fatal(err)
		}
		dag, err := testNode.DaemonAlertsGet()
		if err != nil {
			t.Fatal(err)
		}
		for _, alert := range dag.Alerts {
			if alert.Module == "gateway" && alert.Cause == "" &&
				alert.Msg == gateway.AlertMSGGatewayOffline && alert.Severity == modules.SeverityWarning {
				return nil
			}
		}
		return errors.New("couldn't find correct alert")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Connect nodes.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		return testNode.GatewayConnectPost(testNode2.GatewayAddress())
	})
	if err != nil {
		t.Fatal(err)
	}

	// Test that gateway unregistered alert.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, err := testNode.GatewayGet()
		if err != nil {
			t.Fatal(err)
		}
		dag, err := testNode.DaemonAlertsGet()
		if err != nil {
			t.Fatal(err)
		}
		for _, alert := range dag.Alerts {
			if alert.Module == "gateway" && alert.Cause == "" &&
				alert.Msg == gateway.AlertMSGGatewayOffline && alert.Severity == modules.SeverityWarning {
				return errors.New("alert is still registered")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestGatewayBandwidth checks that the Gateway's bandwidth is being monitored
func TestGatewayBandwidth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create two Gateways
	testDir := gatewayTestDir(t.Name())
	gateway1, err := siatest.NewCleanNode(node.Gateway(testDir + "/gateway1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gateway1.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	gateway2, err := siatest.NewCleanNode(node.Gateway(testDir + "/gateway1"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gateway2.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Both gateways should have no bandwidth usage
	gbg1, err := gateway1.GatewayBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}
	if gbg1.Download != 0 || gbg1.Upload != 0 {
		t.Log("Download:", gbg1.Download)
		t.Log("Upload:", gbg1.Upload)
		t.Fatal("Expected Gateway 1 to have no bandwidth usage")
	}
	gbg2, err := gateway2.GatewayBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}
	if gbg2.Download != 0 || gbg2.Upload != 0 {
		t.Log("Download:", gbg2.Download)
		t.Log("Upload:", gbg2.Upload)
		t.Fatal("Expected Gateway 2 to have no bandwidth usage")
	}

	// Connect the gateways
	err = gateway1.GatewayConnectPost(gateway2.GatewayAddress())
	if err != nil {
		t.Fatal(err)
	}

	// After connecting the gateways, both gateways should have used some upload
	// and download bandwidth
	gbg1, err = gateway1.GatewayBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}
	if gbg1.Download == 0 || gbg1.Upload == 0 {
		t.Log("Download:", gbg1.Download)
		t.Log("Upload:", gbg1.Upload)
		t.Fatal("Expected Gateway 1 to have bandwidth usage after connecting with a peer")
	}
	gbg2, err = gateway2.GatewayBandwidthGet()
	if err != nil {
		t.Fatal(err)
	}
	if gbg2.Download == 0 || gbg2.Upload == 0 {
		t.Log("Download:", gbg2.Download)
		t.Log("Upload:", gbg2.Upload)
		t.Fatal("Expected Gateway 2 to have bandwidth usage after connecting with a peer")
	}
}
