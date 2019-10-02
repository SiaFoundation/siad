package siatest

import (
	"path/filepath"
	"strings"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/node"
)

// TestCreateTestGroup tests the behavior of NewGroup.
func TestNewGroup(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	// Specify the parameters for the group
	groupParams := GroupParams{
		Hosts:   5,
		Renters: 2,
		Miners:  2,
	}
	// Create the group
	tg, err := NewGroupFromTemplate(siatestTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Check if the correct number of nodes was created
	if len(tg.Hosts()) != groupParams.Hosts {
		t.Error("Wrong number of hosts")
	}
	if len(tg.Renters()) != groupParams.Renters {
		t.Error("Wrong number of renters")
	}
	if len(tg.Miners()) != groupParams.Miners {
		t.Error("Wrong number of miners")
	}
	if len(tg.Nodes()) != groupParams.Hosts+groupParams.Renters+groupParams.Miners {
		t.Error("Wrong number of nodes")
	}

	// Check if nodes are funded
	cg, err := tg.Nodes()[0].ConsensusGet()
	if err != nil {
		t.Fatal("Failed to get consensus: ", err)
	}
	for _, node := range tg.Nodes() {
		wtg, err := node.WalletTransactionsGet(0, cg.Height)
		if err != nil {
			t.Fatal(err)
		}
		if len(wtg.ConfirmedTransactions) == 0 {
			t.Errorf("Node has 0 confirmed funds")
		}
	}
}

// TestCreateTestGroup tests NewGroup without a miner
func TestNewGroupNoMiner(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	// Try to create a group without miners
	groupParams := GroupParams{
		Hosts:   5,
		Renters: 2,
		Miners:  0,
	}
	// Create the group
	_, err := NewGroupFromTemplate(siatestTestDir(t.Name()), groupParams)
	if err == nil {
		t.Fatal("Creating a group without miners should fail: ", err)
	}
}

// TestCreateTestGroup tests NewGroup with no renter or host
func TestNewGroupNoRenterHost(t *testing.T) {
	if testing.Short() || !build.VLONG {
		t.SkipNow()
	}
	// Create a group with nothing but miners
	groupParams := GroupParams{
		Hosts:   0,
		Renters: 0,
		Miners:  5,
	}
	// Create the group
	tg, err := NewGroupFromTemplate(siatestTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
}

// TestAddNewNode tests that the added node is returned when AddNodes is called
func TestAddNewNode(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a group
	groupParams := GroupParams{
		Renters: 2,
		Miners:  1,
	}
	tg, err := NewGroupFromTemplate(siatestTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Record current nodes
	oldRenters := tg.Renters()

	// Test adding a node
	testDir := TestDir(t.Name())
	renterTemplate := node.Renter(filepath.Join(testDir, "/renter"))
	nodes, err := tg.AddNodes(renterTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 {
		t.Fatalf("More nodes returned than expected; expected 1 got %v", len(nodes))
	}
	renter := nodes[0]
	for _, oldRenter := range oldRenters {
		if oldRenter.primarySeed == renter.primarySeed {
			t.Fatal("Returned renter is not the new renter")
		}
	}
}

// TestGatewayAddress tests that you can blacklist a peer and use the Gateway
// Address to add a new peer with a new host address
func TestGatewayAddress(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create testgroup
	groupParams := GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  2,
	}
	testDir := siatestTestDir(t.Name())
	tg, err := NewGroupFromTemplate(testDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group:", err)
	}
	defer tg.Close()

	// Grab the renter
	r := tg.Renters()[0]

	// Grab the current peers
	gg, err := r.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}

	// There should be 3 peers
	if len(gg.Peers) != 3 {
		t.Fatalf("Expected %v peers, got %v", 3, len(gg.Peers))
	}

	// Blacklist one of the peers by manually disconnecting from them
	m := tg.Miners()[0]
	gg, err = m.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}
	err = r.GatewayDisconnectPost(gg.NetAddress)
	if err != nil {
		t.Fatal(err)
	}

	// Get the list of renter peers again
	gg, err = r.GatewayGet()
	if err != nil {
		t.Fatal(err)
	}

	// There should be 2 peers
	if len(gg.Peers) != 2 {
		t.Fatalf("Expected %v peers, got %v", 2, len(gg.Peers))
	}

	// Add node to the test group. This should fail because the default nodes
	// have the same local address and since we have blacklisted a previous node
	// that will blacklist future nodes.
	nodeParams := node.Renter(filepath.Join(testDir, "node"))
	_, err = tg.AddNodes(nodeParams)
	if err == nil {
		t.Fatal("Shouldn't be able to add a node")
	}
	if !strings.Contains(err.Error(), "failed to connect to peer") {
		t.Fatal("expected err to contain `failed to connect to peer` but got", err)
	}

	// Add a renter with a unique gateway address, this will work as the default
	// address that was blacklisted is 127.0.0.1
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams.GatewayAddress = "127.0.0.2:"
	_, err = tg.AddNodes(renterParams)
	if err != nil {
		t.Fatal(err)
	}
}
