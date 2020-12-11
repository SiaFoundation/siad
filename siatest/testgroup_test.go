package siatest

import (
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/node"
)

// TestNewGroup tests the behavior of NewGroup.
func TestNewGroup(t *testing.T) {
	if !build.VLONG {
		t.SkipNow()
	}
	// Specify the parameters for the group
	groupParams := GroupParams{
		Hosts:   5,
		Portals: 1,
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
	if len(tg.Portals()) != groupParams.Portals {
		t.Error("Wrong number of portals")
	}
	expectedRenters := groupParams.Portals + groupParams.Renters
	if len(tg.Renters()) != expectedRenters {
		t.Error("Wrong number of renters")
	}
	if len(tg.Portals()) != groupParams.Portals {
		t.Error("Wrong number of portals")
	}
	if len(tg.Miners()) != groupParams.Miners {
		t.Error("Wrong number of miners")
	}
	expectedNumberNodes := groupParams.Hosts + groupParams.Portals + groupParams.Renters + groupParams.Miners
	if len(tg.Nodes()) != expectedNumberNodes {
		t.Error("Wrong number of nodes")
	}

	// Check that all hosts are announced and have a registry.
	for _, host := range tg.Hosts() {
		hg, err := host.HostGet()
		if err != nil {
			t.Fatal(err)
		}
		if !hg.InternalSettings.AcceptingContracts {
			t.Fatal("host not accepting contracts")
		}
		if hg.InternalSettings.RegistrySize == 0 {
			t.Fatal("registry not set")
		}
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

// TestNewGroupNoMiner tests NewGroup without a miner
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

// TestNewGroupNoRenterHost tests NewGroup with no renter or host
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

// TestNewGroupPortal tests NewGroup with a portal
func TestNewGroupPortal(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Initiate a group with hosts and a miner.
	groupParams := GroupParams{
		Hosts:  4,
		Miners: 1,
	}
	// Create the group
	groupDir := siatestTestDir(t.Name())
	tg, err := NewGroupFromTemplate(groupDir, groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Add two portals with allowances that only require 1 host. This tests the
	// portals expecting more contracts than what is defined by the allowance.
	a := DefaultAllowance
	a.Hosts = 1
	portal1Params := node.Renter(filepath.Join(groupDir, "/portal1"))
	portal1Params.CreatePortal = true
	portal1Params.Allowance = a
	portal2Params := node.Renter(filepath.Join(groupDir, "/portal2"))
	portal2Params.CreatePortal = true
	portal2Params.Allowance = a
	_, err = tg.AddNodes(portal1Params, portal2Params)
	if err != nil {
		t.Fatal("Failed to add portals to group: ", err)
	}

	// Grab portals
	portals := tg.Portals()
	p1 := portals[0]
	p2 := portals[1]

	// The Test Group should have the portal listed as a renter as well
	if len(tg.Renters()) != len(portals) {
		t.Fatal("Expected same number of renters and portals")
	}

	// Have 1 portal upload a skyfile and have the other portal download it
	skylink, _, _, err := p1.UploadNewSkyfileBlocking("file", 100, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = p2.SkynetSkylinkGet(skylink)
	if err != nil {
		t.Fatal(err)
	}
}
