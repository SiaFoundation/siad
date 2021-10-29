package daemon

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"

	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/node/api/client"
	"go.sia.tech/siad/profile"
	"go.sia.tech/siad/siatest"
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
	opts, err := client.DefaultOptions()
	if err != nil {
		t.Fatal(err)
	}
	opts.Address = testNode.Server.APIAddress()
	c := client.New(opts)
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
	if err := r.RenterRateLimitPost(10*chunkSize, 10*chunkSize); err != nil {
		t.Fatal(err)
	}
	// Set the daemon's limit to 1 chunk per second.
	if err := r.DaemonGlobalRateLimitPost(chunkSize, chunkSize); err != nil {
		t.Fatal(err)
	}
	// Download the file. It should take at least expectedSeconds seconds.
	start := time.Now()
	if _, _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	timePassed := time.Since(start)
	if timePassed < time.Second*time.Duration(expectedSeconds) {
		t.Errorf("download took %v but should've been at least %v",
			timePassed, time.Second*time.Duration(expectedSeconds))
	}
	// Swap the limits and make sure the limit is still in effect.
	if err := r.RenterRateLimitPost(chunkSize, chunkSize); err != nil {
		t.Fatal(err)
	}
	// Set the daemon's limit to 1 chunk per second.
	if err := r.DaemonGlobalRateLimitPost(10*chunkSize, 10*chunkSize); err != nil {
		t.Fatal(err)
	}
	// Download the file. It should take at least expectedSeconds seconds.
	start = time.Now()
	if _, _, err := r.DownloadByStream(rf); err != nil {
		t.Fatal(err)
	}
	timePassed = time.Since(start)
	if timePassed < time.Second*time.Duration(expectedSeconds) {
		t.Errorf("download took %v but should've been at least %v",
			timePassed, time.Second*time.Duration(expectedSeconds))
	}
}

// TestDaemonConfig verifies that the config values returned from the Settings
// endpoint contains sane values
func TestDaemonConfig(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := daemonTestDir(t.Name())

	// Create a new server with all Modules loaded
	testNode, err := siatest.NewCleanNode(node.AllModules(testDir))
	if err != nil {
		t.Fatal(err)
	}

	// Get the Settings.
	dsg, err := testNode.DaemonSettingsGet()
	if err != nil {
		t.Fatal(err)
	}

	// All the Modules should be set to true expect the Explorer
	if !dsg.Modules.Consensus {
		t.Error("Consensus should be set as true")
	}
	if dsg.Modules.Explorer {
		t.Error("Explorer should be set as false")
	}
	if !dsg.Modules.Gateway {
		t.Error("Gateway should be set as true")
	}
	if !dsg.Modules.Host {
		t.Error("Host should be set as true")
	}
	if !dsg.Modules.Miner {
		t.Error("Miner should be set as true")
	}
	if !dsg.Modules.Renter {
		t.Error("Renter should be set as true")
	}
	if !dsg.Modules.TransactionPool {
		t.Error("TransactionPool should be set as true")
	}
	if !dsg.Modules.Wallet {
		t.Error("Wallet should be set as true")
	}

	// Close server
	if err := testNode.Close(); err != nil {
		t.Fatal(err)
	}

	// Create a new server with only the Gateway
	testNode, err = siatest.NewCleanNode(node.Gateway(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Get the Settings.
	dsg, err = testNode.DaemonSettingsGet()
	if err != nil {
		t.Fatal(err)
	}

	// All the Modules should be set to false except the Gateway
	if dsg.Modules.Consensus {
		t.Error("Consensus should be set as false")
	}
	if dsg.Modules.Explorer {
		t.Error("Explorer should be set as false")
	}
	if !dsg.Modules.Gateway {
		t.Error("Gateway should be set as true")
	}
	if dsg.Modules.Host {
		t.Error("Host should be set as false")
	}
	if dsg.Modules.Miner {
		t.Error("Miner should be set as false")
	}
	if dsg.Modules.Renter {
		t.Error("Renter should be set as false")
	}
	if dsg.Modules.TransactionPool {
		t.Error("TransactionPool should be set as false")
	}
	if dsg.Modules.Wallet {
		t.Error("Wallet should be set as false")
	}
}

// TestDaemonStack test the /dameon/stack endpoint.
func TestDaemonStack(t *testing.T) {
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
		err = testNode.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Get the stack
	dsg, err := testNode.DaemonStackGet()
	if err != nil {
		t.Fatal(err)
	}

	// Make sure the stack is not empty
	if len(dsg.Stack) == 0 {
		t.Fatal("Stack is empt")
	}
}

// TestDaemonProfile test the /dameon/profile endpoint.
func TestDaemonProfile(t *testing.T) {
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
		err = testNode.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// Test known error cases
	err = testNode.DaemonStartProfilePost("", "")
	if !strings.Contains(err.Error(), "profile flags cannot be blank") {
		t.Error("Unexpected error:", err)
	}
	err = testNode.DaemonStartProfilePost("test", "")
	if !strings.Contains(err.Error(), profile.ErrInvalidProfileFlags.Error()) {
		t.Error("Unexpected error:", err)
	}

	// Test Stopping without a profile started
	err = testNode.DaemonStopProfilePost()
	if err != nil {
		t.Fatal(err)
	}

	// Start Profile
	profileDir := filepath.Join(testNode.Dir, "profile")
	err = testNode.DaemonStartProfilePost("cmt", profileDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify profile directory was created
	_, err = os.Stat(profileDir)
	if err != nil {
		t.Fatal(err)
	}

	// Stop Profile
	err = testNode.DaemonStopProfilePost()
	if err != nil {
		t.Fatal(err)
	}
}
