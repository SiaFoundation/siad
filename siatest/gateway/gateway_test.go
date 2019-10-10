package gateway

import (
	"errors"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
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
