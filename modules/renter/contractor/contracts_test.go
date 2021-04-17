package contractor

import (
	"testing"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestPubkeysToContractIDMap tests updating the contractor's
// pubKeysToContractID map
func TestPubkeysToContractIDMap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	h, c, _, cf, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer tryClose(cf, t)

	// The contractor shouldn't have any contracts formed so the pubkey map
	// should be empty
	c.mu.Lock()
	pubKeyMapLen := len(c.pubKeysToContractID)
	c.mu.Unlock()
	if pubKeyMapLen != 0 {
		t.Fatal("pubkey map is not empty")
	}

	// acquire the contract maintenance lock for the duration of the test. This
	// prevents theadedContractMaintenance from running.
	c.maintenanceLock.Lock()
	defer c.maintenanceLock.Unlock()

	// get the host's entry from the db
	pk := h.PublicKey()
	hostEntry, ok, err := c.hdb.Host(pk)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("no entry for host in db")
	}

	// set an allowance but don't use SetAllowance to avoid automatic contract
	// formation.
	c.mu.Lock()
	c.allowance = modules.DefaultAllowance
	c.mu.Unlock()

	// form a contract with the host
	_, contract, err := c.managedNewContract(hostEntry, types.SiacoinPrecision.Mul64(50), c.blockHeight+100)
	if err != nil {
		t.Fatal(err)
	}

	// Call managedUpdatePubKeyToContractIDMap
	c.managedUpdatePubKeyToContractIDMap()

	// Check pubkey map
	c.mu.Lock()
	pubKeyMapLen = len(c.pubKeysToContractID)
	fcid, ok := c.pubKeysToContractID[pk.String()]
	c.mu.Unlock()
	if !ok {
		t.Fatal("Host Pubkey and contract not in map")
	}
	if fcid != contract.ID {
		t.Error("Wrong FileContractID found in map")
	}
	if pubKeyMapLen != 1 {
		t.Fatal("pubkey map should have 1 entry")
	}

	// Add a slice of contracts
	_, pubkey := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pubkey[:],
	}
	c2 := modules.RenterContract{
		ID:            types.FileContractID{'1'},
		HostPublicKey: spk,
	}
	contracts := []modules.RenterContract{c2, contract}
	c.mu.Lock()
	c.updatePubKeyToContractIDMap(contracts)
	c.mu.Unlock()

	// Check pubkey map
	c.mu.Lock()
	pubKeyMapLen = len(c.pubKeysToContractID)
	fcid, ok = c.pubKeysToContractID[pk.String()]
	fcid2, ok2 := c.pubKeysToContractID[spk.String()]
	c.mu.Unlock()
	if !ok {
		t.Fatal("Original Host Pubkey and contract not in map")
	}
	if fcid != contract.ID {
		t.Error("Wrong FileContractID found in map")
	}
	if !ok2 {
		t.Fatal("Second Host Pubkey and contract not in map")
	}
	if fcid2 != c2.ID {
		t.Error("Wrong second FileContractID found in map")
	}
	if pubKeyMapLen != 2 {
		t.Fatal("pubkey map should have 2 entries")
	}
}

// TestTryAddContractToPubKeyMap tests the tryAddContractToPubKeyMap method
func TestTryAddContractToPubKeyMap(t *testing.T) {
	// Create minimum Contractor
	c := &Contractor{
		renewedTo:           make(map[types.FileContractID]types.FileContractID),
		pubKeysToContractID: make(map[string]types.FileContractID),
	}

	// Create minimum contracts
	_, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	c1 := modules.RenterContract{
		ID:            types.FileContractID{'1'},
		HostPublicKey: spk,
	}
	_, pk = crypto.GenerateKeyPair()
	spk = types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}
	c2 := modules.RenterContract{
		ID:            types.FileContractID{'2'},
		HostPublicKey: spk,
	}

	// Add c1 to renewTo map
	c.renewedTo[c1.ID] = c2.ID

	// Try and add c1 to pubKey map, should not be added
	c.tryAddContractToPubKeyMap(c1)
	if len(c.pubKeysToContractID) != 0 {
		t.Error("PubKey map should be empty")
	}

	// Adding c2 should work
	c.tryAddContractToPubKeyMap(c2)
	if len(c.pubKeysToContractID) != 1 {
		t.Error("PubKey map should have 1 entry")
	}
	// Can't test the case of a pubkey already in the pubkey map as that results
	// in a Critical log
}
