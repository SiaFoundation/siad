package daemon

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestDaemonRatelimit makes sure that we can set the daemon's global
// ratelimits using the API and that they are persisted correctly.
func TestDaemonRatelimit(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := daemonTestDir(t.Name())

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
	dsg, err := testNode.DaemonSettingsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Speeds should be 0 which means it's not being rate limited.
	if dsg.MaxDownloadSpeed != 0 || dsg.MaxUploadSpeed != 0 {
		t.Fatalf("Limits should be 0 but were %v and %v", dsg.MaxDownloadSpeed, dsg.MaxUploadSpeed)
	}
	// Change the limits.
	ds := int64(100)
	us := int64(200)
	if err := testNode.DaemonGlobalRateLimitPost(ds, us); err != nil {
		t.Fatal(err)
	}
	// Get the ratelimit again.
	dsg, err = testNode.DaemonSettingsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Limit should be set correctly.
	if dsg.MaxDownloadSpeed != ds || dsg.MaxUploadSpeed != us {
		t.Fatalf("Limits should be %v/%v but are %v/%v",
			ds, us, dsg.MaxDownloadSpeed, dsg.MaxUploadSpeed)
	}
	// Restart the node.
	if err := testNode.RestartNode(); err != nil {
		t.Fatal(err)
	}
	// Get the ratelimit again.
	dsg, err = testNode.DaemonSettingsGet()
	if err != nil {
		t.Fatal(err)
	}
	// Limit should've been persisted correctly.
	if dsg.MaxDownloadSpeed != ds || dsg.MaxUploadSpeed != us {
		t.Fatalf("Limits should be %v/%v but are %v/%v",
			ds, us, dsg.MaxDownloadSpeed, dsg.MaxUploadSpeed)
	}
}
