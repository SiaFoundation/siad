package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/persist"
	siaPersist "gitlab.com/NebulousLabs/Sia/persist"
)

const numTestingGateways = 3

const testPersistFile = "testdata/persisted-node-set.json"

// TestLoad checks that the testdata set is loaded with sane values.
func TestLoad(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	now := time.Now()
	data := persistData{
		StartTime: now,
		NodeStats: make(map[modules.NetAddress]nodeStats),
	}

	err := siaPersist.LoadJSON(persistMetadata, &data, testPersistFile)
	if err != nil {
		t.Fatal("Error loading persisted node set: ", err)
	}

	// Make sure StartTime has a reasonable (i.e. nonzero) value.
	if data.StartTime.IsZero() {
		t.Fatal("Expected nonzero StartTime value")
	}

	// Make sure the data set is non-empty.
	if len(data.NodeStats) == 0 {
		t.Fatal("Expected nonzero NodeStats")
	}

	// Check that all structs have nonzero values.
	// This makes sure that we exported all the fields for NodeStats.
	ok := true
	for addr, nodeStats := range data.NodeStats {
		if addr == "" {
			ok = false
		}
		if nodeStats.FirstConnectionTime.IsZero() {
			ok = false
		}
		if nodeStats.LastSuccessfulConnectionTime.IsZero() {
			ok = false
		}
		if nodeStats.RecentUptime == 0 {
			ok = false
		}
		if nodeStats.TotalUptime == 0 {
			ok = false
		}
		if nodeStats.UptimePercentage == 0.0 {
			ok = false
		}
		if !ok {
			t.Fatal("Expected nonzero fields in NodeStats: ", addr, nodeStats)
		}
	}
}

// Spin up several gateways and connect them to each other, then check that
// sendShareNodesRequests returns the expected results.
func TestSendShareNodesRequests(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	mainGateway, err := gateway.New("localhost:0", true, build.TempDir("SiaNodeScannerTestGateway"))
	if err != nil {
		t.Fatal("Error making new gateway: ", err)
	}
	defer mainGateway.Close()

	// Create testing gateways.
	gateways := make([]*gateway.Gateway, 0, numTestingGateways)
	for i := 0; i < numTestingGateways; i++ {
		g, err := gateway.New("localhost:0", true, build.TempDir(fmt.Sprintf("SiaNodeScannerTestGateway-%d", i)))
		if err != nil {
			t.Fatal("Error making new gateway: ", err)
		}
		gateways = append(gateways, g)
		defer g.Close()
	}

	// Connect the the 0th testing gateway to all the other ones.
	for i := 1; i < numTestingGateways; i++ {
		err := gateways[i].Connect(gateways[0].Address())
		if err != nil {
			t.Fatal("Error connecting testing gateways: ", err)
		}
	}
	// Connect main gateway to the 0th testing gateway.
	err = mainGateway.Connect(gateways[0].Address())
	if err != nil {
		t.Fatal("Error connecting testing gateways: ", err)
	}

	// Test the sendShareNodesRequests function by making sure we get at least 10
	// peers from the 0th testing gateway.
	work := workAssignment{
		node: gateways[0].Address(),
	}
	res := sendShareNodesRequests(mainGateway, work)

	if res.Err != nil {
		t.Fatal("Error from sendShareNodesRequests: ", err)
	}
	if res.Addr != gateways[0].Address() {
		t.Fatal("Expected result address to match workAssignment address")
	}
	if len(res.nodes) != numTestingGateways {
		// ShareNodes will return mainGateway and the other nodes in gateways.
		t.Fatalf("Expected %d nodes from ShareNodes, but got %d instead\n", numTestingGateways, len(res.nodes))
	}
}

// TestRestartScanner creates a nodeScanner and starts it from a faked persisted
// set created using testing gateways.  It then checks that values in the
// persisted set are sanely updated when the node scanner restarts from an
// existing set.
func TestRestartScanner(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	testDir := build.TempDir("SiaNodeScanner-TestRestartScanner")
	err := os.Mkdir(testDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal("Error creating testing directory: ", err)
	}

	gateways := make([]*gateway.Gateway, 0, numTestingGateways)
	gatewayAddrs := make([]modules.NetAddress, 0, numTestingGateways)
	for i := 0; i < numTestingGateways; i++ {
		g, err := gateway.New(fmt.Sprintf("localhost:4444%d", i), true, build.TempDir(fmt.Sprintf("SiaNodeScannerTestGateway-%d", i)))
		if err != nil {
			t.Fatal("Error making new gateway: ", err)
		}
		gateways = append(gateways, g)
		gatewayAddrs = append(gatewayAddrs, g.Address())
	}

	// Create the testing node scanner.
	ns := newNodeScanner(testDir)
	defer ns.gateway.Close()

	// Create a fake persisted set file, using the testing gateway addresses.
	err = ns.setupPersistFile(ns.persistFile)
	if err != nil {
		t.Fatal("Error when creating persist")
	}
	recentPast := time.Now().Add(-10 * time.Minute)
	testData := persistData{
		StartTime: recentPast,
		NodeStats: make(map[modules.NetAddress]nodeStats),
	}
	for _, g := range gateways {
		testData.NodeStats[g.Address()] = nodeStats{
			FirstConnectionTime:          recentPast,
			LastSuccessfulConnectionTime: recentPast,
			RecentUptime:                 1,
			TotalUptime:                  1,
			UptimePercentage:             100.0,
		}
	}
	ns.data = testData
	err = ns.persistData()
	if err != nil {
		t.Fatal("Unexpected persist error: ", err)
	}

	// Get the fake data into the nodeScanner work queues.
	ns.initialize()

	// Shutdown the odd indexed gateways so the scan fails on those addresses.
	// Defer shutdown for the other gateways.
	for i := 0; i < numTestingGateways; i++ {
		if i%2 == 1 {
			gateways[i].Close()
		} else {
			defer gateways[i].Close()
		}
	}

	// Start a scan across the testing gateways.
	ns.startScan()

	// Persist the data, and load it into a new struct to see if the test scan
	// affected the persisted set.
	ns.persistData()
	var testData2 persistData
	err = siaPersist.LoadJSON(persistMetadata, &testData2, ns.persistFile)
	if err != nil {
		t.Fatal("error loading persist after scan: ", err)
	}

	// Check the new testData is updated properly.
	for i := 0; i < numTestingGateways; i++ {
		stats := testData2.NodeStats[gatewayAddrs[i]]
		if i%2 == 0 {
			if !stats.LastSuccessfulConnectionTime.After(stats.FirstConnectionTime) {
				t.Fatal("Expected test scan to update connection time", i, stats)
			}
			if stats.RecentUptime < stats.TotalUptime {
				t.Fatal("Expected recent uptime to match total uptime if scan succeeded", i, stats)
			}
			if stats.UptimePercentage < 100.0 {
				t.Fatal("Expected perfect uptime", i, stats)
			}
		} else {
			if stats.LastSuccessfulConnectionTime.After(stats.FirstConnectionTime) {
				t.Fatal("Expected test scan not to update connection time", i, stats)
			}
			if stats.RecentUptime != 0 {
				t.Fatal("Expected recent uptime to go to 0 for failed connection", i, stats)
			}
			if stats.UptimePercentage >= 10.0 {
				t.Fatal("Expected lower uptime", i, stats)
			}
		}
	}
}
