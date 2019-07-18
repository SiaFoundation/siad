package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	siaPersist "gitlab.com/NebulousLabs/Sia/persist"
)

// 1 gateway + 10 peers
const numTestingGateways = 10

const testPersistFile = "testdata/persisted-node-set.json"

// Check that the testdata set is loaded with sane values.
func TestLoad(t *testing.T) {
	data := persistData{
		StartTime: time.Now().Unix(),
		NodeStats: make(map[modules.NetAddress]nodeStats),
	}

	err := siaPersist.LoadJSON(metadata, &data, testPersistFile)
	if err != nil {
		t.Fatal("Error loading persisted node set: ", err)
	}

	// Make sure StartTime has a reasonable (i.e. nonzero) value.
	if data.StartTime == 0 {
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
		if nodeStats.FirstConnectionTime == 0 {
			ok = false
		}
		if nodeStats.LastSuccessfulConnectionTime == 0 {
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
	mainGateway, err := gateway.New("localhost:0", true, build.TempDir("SiaNodeScannerTestGateway"))
	if err != nil {
		t.Fatal("Error making new gateway: ", err)
	}

	// Create testing gateways.
	gateways := make([]*gateway.Gateway, 0, numTestingGateways)
	for i := 0; i < numTestingGateways; i++ {
		g, err := gateway.New("localhost:0", true, build.TempDir(fmt.Sprintf("SiaNodeScannerTestGateway-%d", i)))
		if err != nil {
			t.Fatal("Error making new gateway: ", err)
		}
		gateways = append(gateways, g)
	}

	// Connect the the 0th testing gateway to all the other ones.
	for i := 1; i < numTestingGateways; i++ {
		err := gateways[0].Connect(gateways[i].Address())
		if err != nil {
			t.Fatal("Error connecting testing gateways: ", err)
		}
	}
	// Connect main gateway to the 0th testing gateway.
	err = mainGateway.Connect(gateways[0].Address())
	if err != nil {
		t.Fatal("Error connecting testing gateways: ", err)
	}

	// Sleep for a few seconds so the ShareNodes RPCs return the expected result.
	time.Sleep(5 * time.Second)

	// Test the sendShareNodesRequests function by making sure we get at least 10
	// peers from the 0th testing gateway.
	work := workAssignment{
		node:           gateways[0].Address(),
		maxRPCAttempts: 10,
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

// Check that values in the persisted set are sanely updated when the node
// scanner restarts from an existing set.
func TestRestartScanner(t *testing.T) {
	testDir := build.TempDir("SiaNodeScanner-TestRestartScanner")
	err := os.Mkdir(testDir, 0777)
	if err != nil {
		t.Fatal("Error creating testing directory: ", err)
	}

	// Create testing gateways.
	mainGateway, err := gateway.New("localhost:0", true, build.TempDir("SiaNodeScannerTestGateway"))
	if err != nil {
		t.Fatal("Error making new gateway: ", err)
	}
	gateways := make([]*gateway.Gateway, 0, numTestingGateways)
	gatewayAddrs := make([]modules.NetAddress, 0, numTestingGateways)
	for i := 0; i < numTestingGateways; i++ {
		g, err := gateway.New("localhost:0", true, build.TempDir(fmt.Sprintf("SiaNodeScannerTestGateway-%d", i)))
		if err != nil {
			t.Fatal("Error making new gateway: ", err)
		}
		gateways = append(gateways, g)
		gatewayAddrs = append(gatewayAddrs, g.Address())
	}

	// Create a fake persisted set file, using the testing gateway addresses.
	recentPast := time.Now().Unix() - 10000
	persistFile := filepath.Join(testDir, "persisted-node-set.json")
	persist, err := newPersist(persistFile)
	if err != nil {
		t.Fatal("Error when creating persist")
	}
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
	persist.data = testData
	err = persist.persistData()
	if err != nil {
		t.Fatal("Unexpected persist error: ", err)
	}

	// Connect the the 0th testing gateway to all the even indexed gateways and
	// shutdown the odd indexed gateways.
	for i := 0; i < numTestingGateways; i++ {
		if i%2 == 0 {
			err := mainGateway.Connect(gateways[i].Address())
			if err != nil {
				t.Fatal("Error connecting testing gateways: ", err)
			}
		} else {
			gateways[i].Close()
		}
	}
	// Wait for connections to establish.
	time.Sleep(2 * time.Second)

	// Simulate a scan across the testing gateways.
	// Only one RPC is sent to get connection status.
	results := make([]nodeScanResult, 0, numTestingGateways)
	for i := 0; i < numTestingGateways; i++ {
		work := workAssignment{
			node:           gatewayAddrs[i],
			maxRPCAttempts: 1,
		}
		res := sendShareNodesRequests(mainGateway, work)
		if i%2 == 0 {
			if res.Err != nil {
				t.Fatal("Expected succesful connection with even indexed gaterway: ", err)
			}
		} else {
			if res.Err == nil {
				t.Fatal("Expected unsuccessful connection with odd indexed gateway: ", err)
			}
		}
		persist.updateNodeStats(res)
		results = append(results, res)
	}

	// Persist the data, and load it into a new struct to see if the test scan
	// affected the persisted set.
	persist.persistData()
	var testData2 persistData
	err = siaPersist.LoadJSON(metadata, &testData2, persistFile)
	if err != nil {
		t.Fatal("error loading persist after scan: ", err)
	}

	// Check the new testData is updated properly.
	for i := 0; i < numTestingGateways; i++ {
		stats := testData2.NodeStats[gatewayAddrs[i]]
		if i%2 == 0 {
			if stats.LastSuccessfulConnectionTime <= stats.FirstConnectionTime {
				t.Fatal("Expected test scan to update connection time", i, stats)
			}
			if stats.RecentUptime != stats.TotalUptime {
				t.Fatal("Expected recent uptime to match total uptime if scan succeeded", i, stats)
			}
			if stats.UptimePercentage < 100.0 {
				t.Fatal("Expected perfect uptime", i, stats)
			}
		} else {
			if stats.LastSuccessfulConnectionTime > stats.FirstConnectionTime {
				t.Fatal("Expected test scan not to update connection time", i, stats)
			}
			if stats.RecentUptime != 0 {
				t.Fatal("Expected recent uptime to go to 0 for failed connection", i, stats)
			}
			if stats.UptimePercentage >= 99.0 {
				t.Fatal("Expected lower uptime", i, stats)
			}
		}
	}
}
