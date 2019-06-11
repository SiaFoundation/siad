package daemon

import (
	"encoding/hex"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/node/api/client"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
)

// TestDaemonAPIPassword makes sure that the daemon rejects requests with the
// wrong API password.
func TestDaemonAPIPassword(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := daemonTestDir(t.Name())

	// Create a new server
	testNode, err := siatest.NewCleanNode(node.Gateway(testDir))
	if err != nil {
		t.Fatal(err)
	}

	// Make a manual API request without a password.
	c := client.New(testNode.Server.APIAddress())
	if err := c.DaemonStopGet(); err == nil {
		t.Error("expected unauthenticated API request to fail")
	}
	// Make a manual API request with an incorrect password.
	c.Password = hex.EncodeToString(fastrand.Bytes(16))
	if err := c.DaemonStopGet(); err == nil {
		t.Error("expected unauthenticated API request to fail")
	}
	// Make a manual API request with the correct password.
	c.Password = testNode.Password
	if err := c.DaemonStopGet(); err != nil {
		t.Error(err)
	}
}

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

// TestGlobalRatelimitRenter makes sure that if multiple ratelimits are set, the
// lower one is respected.
func TestGlobalRatelimitRenter(t *testing.T) {
	if !build.VLONG || testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a testgroup,
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Renters: 1,
		Miners:  1,
	}
	testDir := daemonTestDir(t.Name())
	tg, err := siatest.NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Grab Renter and upload file
	r := tg.Renters()[0]
	dataPieces := uint64(1)
	parityPieces := uint64(1)
	chunkSize := int64(siatest.ChunkSize(dataPieces, crypto.TypeDefaultRenter))
	expectedSeconds := 10
	fileSize := expectedSeconds * int(chunkSize)
	_, rf, err := r.UploadNewFileBlocking(fileSize, dataPieces, parityPieces, false)
	if err != nil {
		t.Fatal(err)
	}

	// Set the bandwidth limit to 10 chunks per second.
	if err := r.RenterPostRateLimit(10*chunkSize, 10*chunkSize); err != nil {
		t.Fatal(err)
	}
	// Set the daemon's limit to 1 chunk per second.
	if err := r.DaemonGlobalRateLimitPost(chunkSize, chunkSize); err != nil {
		t.Fatal(err)
	}
	// Download the file. It should take at least expectedSeconds seconds.
	start := time.Now()
	if _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	timePassed := time.Since(start)
	if timePassed < time.Second*time.Duration(expectedSeconds) {
		t.Fatalf("download took %v but should've been at least %v",
			timePassed, time.Second*time.Duration(expectedSeconds))
	}
	// Swap the limits and make sure the limit is still in effect.
	if err := r.RenterPostRateLimit(chunkSize, chunkSize); err != nil {
		t.Fatal(err)
	}
	// Set the daemon's limit to 1 chunk per second.
	if err := r.DaemonGlobalRateLimitPost(10*chunkSize, 10*chunkSize); err != nil {
		t.Fatal(err)
	}
	// Download the file. It should take at least expectedSeconds seconds.
	start = time.Now()
	if _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	timePassed = time.Since(start)
	if timePassed < time.Second*time.Duration(expectedSeconds) {
		t.Fatalf("download took %v but should've been at least %v",
			timePassed, time.Second*time.Duration(expectedSeconds))
	}
}
