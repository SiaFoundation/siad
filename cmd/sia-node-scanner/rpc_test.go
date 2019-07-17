package main

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
)

// 1 gateway + 10 peers
const numTestingGateways = 10

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
		fmt.Println(i)
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
	time.Sleep(3 * time.Second)

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
