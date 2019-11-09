package contractor

import (
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

type tpoolGate struct {
	mu         sync.Mutex
	gateClosed bool
	logTxns    bool
	txnSets    [][]types.Transaction
}

// gatedTpool is a transaction pool that can be toggled off silently.
// This is used to simulate failures in transaction propagation. Closing the
// gate also allows for simpler testing in which formation transaction sets are
// clearly invalid, but the watchdog won't be notified by the transaction pool
// if we so choose.
type gatedTpool struct {
	*tpoolGate
	transactionPool
}

func (gp gatedTpool) AcceptTransactionSet(txnSet []types.Transaction) error {
	gp.mu.Lock()
	gateClosed := gp.gateClosed
	logTxns := gp.logTxns
	if logTxns {
		gp.txnSets = append(gp.txnSets, txnSet)
	}
	gp.mu.Unlock()

	if gateClosed {
		return nil
	}
	return gp.transactionPool.AcceptTransactionSet(txnSet)
}

func createFakeRevisionTxn(fcID types.FileContractID, revNum uint64, windowStart, windowEnd types.BlockHeight) types.Transaction {
	rev := types.FileContractRevision{
		ParentID:          fcID,
		NewRevisionNumber: revNum,
		NewWindowStart:    windowStart,
		NewWindowEnd:      windowEnd,
	}

	return types.Transaction{
		FileContractRevisions: []types.FileContractRevision{rev},
	}
}

func createFakeFormationTxnSet(name string) []types.Transaction {
	fc := types.FileContract{
		FileMerkleRoot: crypto.HashObject(name),
	}
	return []types.Transaction{
		{
			FileContracts: []types.FileContract{fc},
		},
	}
}

// Creates transaction tree with many root transactions, a "subroot" transaction
// that spends all the root transaction outputs, and a chain of transactions
// spending the subroot transaction's output.
//
// Visualized: (each '+' is a transaction)
//
// +      +        +          +       +        +
// |      |        |          |       |        |
// |      |        |    ...   |       |        |
// |      |        |          |       |        |
// ----------------------+----------------------
//                       |
//                       +
//                       |
//                       .
//                       .
//                       .
//                       |
//                       +
//
func createTestTransactionTree(numRoots int, chainLength int) (txnSet []types.Transaction, roots []types.Transaction, subRootTx types.Transaction, fcTxn types.Transaction, rootParentOutputs map[types.SiacoinOutputID]bool) {
	roots = make([]types.Transaction, 0, numRoots)
	rootParents := make(map[types.SiacoinOutputID]bool)

	// All the root txs create outputs spent in subRootTx.
	subRootTx = types.Transaction{
		SiacoinInputs: make([]types.SiacoinInput, 0, numRoots),
		SiacoinOutputs: []types.SiacoinOutput{
			{UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32)))},
		},
	}
	for i := 0; i < numRoots; i++ {
		nextRootTx := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{
				{
					ParentID: types.SiacoinOutputID(crypto.HashObject(fastrand.Bytes(32))),
				},
			},
			SiacoinOutputs: []types.SiacoinOutput{
				{UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32)))},
			},
		}
		rootParents[nextRootTx.SiacoinInputs[0].ParentID] = true

		subRootTx.SiacoinInputs = append(subRootTx.SiacoinInputs, types.SiacoinInput{
			ParentID: nextRootTx.SiacoinOutputID(0),
		})
		roots = append(roots, nextRootTx)
	}
	txnSet = append([]types.Transaction{subRootTx}, roots...)

	subRootTx = txnSet[0]
	subRootSet := getParentOutputIDs(txnSet)
	// Sanity check on the subRootTx.
	for _, oid := range subRootSet {
		if !rootParents[oid] {
			panic("non root in parent set")
		}
	}

	// Now create a straight chain of transactions below subRootTx.
	for i := 0; i < chainLength; i++ {
		txnSet = addChildren(txnSet, 1, true)
		// Only the subroottx is supposed to have more than one input.
		for i := 0; i < len(txnSet); i++ {
			if len(txnSet[i].SiacoinInputs) > 1 {
				if txnSet[i].ID() != subRootTx.ID() {
					panic("non subroottx with more than one input")
				}
			}
		}
	}

	// Create a file contract transaction at the very bottom of this transaction
	// tree.
	fcTxn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{ParentID: txnSet[0].SiacoinOutputID(0)}},
		FileContracts: []types.FileContract{{UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32)))}},
	}
	txnSet = append([]types.Transaction{fcTxn}, txnSet...)

	// Sanity Check on the txnSet:
	// Only the subroottx is supposed to have more than one input.
	for i := 0; i < len(txnSet); i++ {
		if len(txnSet[i].SiacoinInputs) > 1 {
			if txnSet[i].ID() != subRootTx.ID() {
				panic("non subroottx with more than one input")
			}
		}
	}

	return txnSet, roots, subRootTx, fcTxn, rootParents
}

func addChildren(txnSet []types.Transaction, numDependencies int, hasOutputs bool) []types.Transaction {
	newTxns := make([]types.Transaction, 0, numDependencies)

	// Create new parent outputs.
	if !hasOutputs {
		for i := 0; i < numDependencies; i++ {
			txnSet[0].SiacoinOutputs = append(txnSet[0].SiacoinOutputs, types.SiacoinOutput{
				UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32))),
			})
		}
	}

	for i := 0; i < numDependencies; i++ {
		newChild := types.Transaction{
			SiacoinInputs:  make([]types.SiacoinInput, 1),
			SiacoinOutputs: make([]types.SiacoinOutput, 0),
		}

		newChild.SiacoinInputs[0] = types.SiacoinInput{
			ParentID: txnSet[0].SiacoinOutputID(uint64(i)),
		}

		newChild.SiacoinOutputs = append(newChild.SiacoinOutputs, types.SiacoinOutput{
			UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32))),
		})

		newTxns = append([]types.Transaction{newChild}, newTxns...)
	}

	return append(newTxns, txnSet...)
}

// TestWatchdogRevisionCheck checks that the watchdog is monitoring the correct
// contract for the relevant revisions, and that it attempts to broadcast the
// latest revision if it hasn't observed it yet on-chain.
func TestWatchdogRevisionCheck(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	_, c, m, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract with the host
	a := modules.Allowance{
		Funds:              types.SiacoinPrecision.Mul64(100), // 100 SC
		Hosts:              1,
		Period:             50,
		RenewWindow:        10,
		ExpectedStorage:    modules.DefaultAllowance.ExpectedStorage,
		ExpectedUpload:     modules.DefaultAllowance.ExpectedUpload,
		ExpectedDownload:   modules.DefaultAllowance.ExpectedDownload,
		ExpectedRedundancy: modules.DefaultAllowance.ExpectedRedundancy,
	}
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 200*time.Millisecond, func() error {
		if len(c.Contracts()) == 0 {
			return errors.New("contracts were not formed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := c.Contracts()[0]

	// Give the watchdog a gated transaction pool, just to log transactions it
	// sends.
	gatedTpool := gatedTpool{
		tpoolGate: &tpoolGate{gateClosed: false,
			logTxns: true,
			txnSets: make([][]types.Transaction, 0, 0),
		},
		transactionPool: c.tpool,
	}
	c.staticWatchdog.mu.Lock()
	c.staticWatchdog.tpool = gatedTpool
	c.staticWatchdog.mu.Unlock()

	// Mine a block, and check that the watchdog finds it, and is watching for the
	// revision.
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	c.staticWatchdog.mu.Lock()
	contractData, ok := c.staticWatchdog.contracts[contract.ID]
	if !ok {
		t.Fatal("Contract not found")
	}
	found := contractData.contractFound
	revFound := contractData.revisionFound
	revHeight := contractData.windowStart - c.staticWatchdog.renewWindow
	c.staticWatchdog.mu.Unlock()

	if !found || (revFound != 0) {
		t.Fatal("Expected to find contract in watchdog watch-revision state")
	}

	fcr := contract.Transaction.FileContractRevisions[0]
	if contractData.windowStart != fcr.NewWindowStart || contractData.windowEnd != fcr.NewWindowEnd {
		t.Fatal("fileContractStatus and initial revision have differing storage proof window")
	}

	// Do several revisions on the contract. Check that watchdog is notified.
	numRevisions := 6 // 1 no-op revision at start + 5 in this loop.

	var lastRevisionTxn types.Transaction
	// Store the intermediate revisions to post on-chain.
	intermediateRevisions := make([]types.Transaction, 0)
	for i := 0; i < numRevisions-1; i++ {
		// revise the contract
		editor, err := c.Editor(contract.HostPublicKey, nil)
		if err != nil {
			t.Fatal(err)
		}
		data := fastrand.Bytes(int(modules.SectorSize))
		_, err = editor.Upload(data)
		if err != nil {
			t.Fatal(err)
		}
		err = editor.Close()
		if err != nil {
			t.Fatal(err)
		}

		newContractState, ok := c.staticContracts.Acquire(contract.ID)
		if !ok {
			t.Fatal("Contract should not have been removed from set")
		}
		intermediateRevisions = append(intermediateRevisions, newContractState.Metadata().Transaction)
		// save the last revision transaction
		if i == numRevisions-2 {
			lastRevisionTxn = newContractState.Metadata().Transaction
		}
		c.staticContracts.Return(newContractState)
	}

	// Mine until the height the watchdog is supposed to post the latest revision by
	// itself. Along the way, make sure it doesn't act earlier than expected.
	// (10 initial blocks + 1 mined in this test)
	for i := 11; i < int(revHeight); i++ {
		// Send out the 0th, 2nd, and 4th revisions, but not the most recent one.
		if i == 0 || i == 2 || i == 4 {
			gatedTpool.transactionPool.AcceptTransactionSet([]types.Transaction{intermediateRevisions[i]})
		}

		_, err = m.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
		gatedTpool.mu.Lock()
		numTxnsPosted := len(gatedTpool.txnSets)
		gatedTpool.mu.Unlock()
		if numTxnsPosted != 0 {
			t.Fatal("watchdog should not have sent any transactions yet", numTxnsPosted)
		}

		// Check the watchdog internal state to make sure it has seen some revisions
		if i == 0 || i == 2 || i == 4 {
			c.staticWatchdog.mu.Lock()
			contractData, ok := c.staticWatchdog.contracts[contract.ID]
			if !ok {
				t.Fatal("Expected to find contract")
			}

			revNum := contractData.revisionFound
			if revNum == 0 {
				t.Fatal("Expected watchdog to find revision")
			}
			// Add 1 to i to get revNum because there is a no-op revision from
			// formation.
			if revNum != uint64(i+1) {
				t.Fatal("Expected different revision number", revNum, i+1)
			}
			c.staticWatchdog.mu.Unlock()
		}
	}

	// Mine one more block, and see that the watchdog has posted the revision
	// transaction.
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	var revTxn types.Transaction
	err = build.Retry(10, time.Second, func() error {
		gatedTpool.mu.Lock()
		defer gatedTpool.mu.Unlock()

		if len(gatedTpool.txnSets) < 1 {
			return errors.New("Expected at least one txn")
		}
		foundRevTxn := false
		for _, txnSet := range gatedTpool.txnSets {
			if (len(txnSet) != 1) || (len(txnSet[0].FileContractRevisions) != 1) {
				continue
			}
			revTxn = txnSet[0]
			foundRevTxn = true
			break
		}

		if !foundRevTxn {
			return errors.New("did not find transaction with revision")
		}
		return nil
	})
	gatedTpool.mu.Lock()

	if err != nil {
		t.Fatal("watchdog should have sent exactly one transaction with a revision", err)
	}

	if revTxn.FileContractRevisions[0].ParentID != contract.ID {
		t.Fatal("watchdog revision sent for wrong contract ID")
	}
	if revTxn.FileContractRevisions[0].NewRevisionNumber != uint64(numRevisions) {
		t.Fatal("watchdog sent wrong revision number")
	}
	gatedTpool.mu.Unlock()

	// Check that the watchdog finds its own revision.
	for i := 0; i < 5; i++ {
		_, err = m.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	c.staticWatchdog.mu.Lock()
	contractData, ok = c.staticWatchdog.contracts[contract.ID]
	if !ok {
		t.Fatal("Expected watchdog to have found a revision")
	}
	revFound = contractData.revisionFound
	c.staticWatchdog.mu.Unlock()

	if revFound == 0 {
		t.Fatal("expected watchdog to have found a revision")
	}

	// LastRevisionTxn was saved when creating all the new revisions.
	lastRevisionNumber := lastRevisionTxn.FileContractRevisions[0].NewRevisionNumber
	if revFound != lastRevisionNumber {
		t.Fatal("Expected watchdog to have found the most recent revision, that it posted")
	}

	// Create a fake reorg and remove the file contract revision.
	c.staticWatchdog.mu.Lock()
	revertedBlock := types.Block{
		Transactions: []types.Transaction{lastRevisionTxn},
	}
	c.staticWatchdog.mu.Unlock()

	revertedCC := modules.ConsensusChange{
		RevertedBlocks: []types.Block{revertedBlock},
	}
	c.staticWatchdog.callScanConsensusChange(revertedCC)

	c.staticWatchdog.mu.Lock()
	contractData, ok = c.staticWatchdog.contracts[contract.ID]
	if !ok {
		t.Fatal("Expected watchdog to have found a revision")
	}
	revFound = contractData.revisionFound
	c.staticWatchdog.mu.Unlock()

	if revFound != 0 {
		t.Fatal("Expected to find contract in watchdog watching state, not found", ok, revFound)
	}
}

// TestWatchdogStorageProofCheck tests that the watchdog correctly notifies the
// contractor when a storage proof is not found in time. Currently the
// watchdog doesn't take any actions, so this test must be updated when that
// functionality is implemented
func TestWatchdogStorageProofCheck(t *testing.T) {
	t.SkipNow()

	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// create testing trio
	_, c, m, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// form a contract with the host
	a := modules.Allowance{
		Funds:              types.SiacoinPrecision.Mul64(100), // 100 SC
		Hosts:              1,
		Period:             30,
		RenewWindow:        10,
		ExpectedStorage:    modules.DefaultAllowance.ExpectedStorage,
		ExpectedUpload:     modules.DefaultAllowance.ExpectedUpload,
		ExpectedDownload:   modules.DefaultAllowance.ExpectedDownload,
		ExpectedRedundancy: modules.DefaultAllowance.ExpectedRedundancy,
	}
	err = c.SetAllowance(a)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(50, 100*time.Millisecond, func() error {
		if len(c.Contracts()) == 0 {
			return errors.New("contracts were not formed")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	contract := c.Contracts()[0]

	// Give the watchdog a gated transaction pool.
	gatedTpool := gatedTpool{
		tpoolGate: &tpoolGate{gateClosed: true,
			logTxns: true,
			txnSets: make([][]types.Transaction, 0, 0),
		},
		transactionPool: c.tpool,
	}
	c.staticWatchdog.mu.Lock()
	c.staticWatchdog.tpool = gatedTpool
	c.staticWatchdog.mu.Unlock()

	// Mine a block, and check that the watchdog finds it, and is watching for the
	// revision.
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	c.staticWatchdog.mu.Lock()
	contractData, ok := c.staticWatchdog.contracts[contract.ID]
	if !ok {
		t.Fatal("Expected watchdog to have found a revision")
	}
	found := contractData.contractFound
	revFound := contractData.revisionFound
	proofFound := contractData.storageProofFound != 0
	storageProofHeight := contractData.windowEnd
	c.staticWatchdog.mu.Unlock()

	if !found || revFound != 0 || proofFound {
		t.Fatal("Expected to find contract in watchdog watch-revision state")
	}
	fcr := contract.Transaction.FileContractRevisions[0]
	if contractData.windowStart != fcr.NewWindowStart || contractData.windowEnd != fcr.NewWindowEnd {
		t.Fatal("fileContractStatus and initial revision have differing storage proof window")
	}

	for i := 11; i < int(storageProofHeight)-1; i++ {
		_, err = m.AddBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	c.staticWatchdog.mu.Lock()
	contractData, ok = c.staticWatchdog.contracts[contract.ID]
	if !ok {
		t.Fatal("Expected watchdog to have found a revision")
	}
	found = contractData.contractFound
	revFound = contractData.revisionFound
	proofFound = contractData.storageProofFound != 0
	c.staticWatchdog.mu.Unlock()

	// The watchdog shuold have posted a revision, and should be watching for the
	// proof still.
	if !found || (revFound == 0) || proofFound {
		t.Fatal("Watchdog should be only watching for a storage proof now")
	}

	// Mine one more block, and see that the watchdog has posted the revision
	// transaction.
	_, err = m.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	err = build.Retry(50, 100*time.Millisecond, func() error {
		contractStatus, ok := c.ContractStatus(contract.ID)
		if !ok {
			return errors.New("no contract status")
		}
		if contractStatus.StorageProofFoundAtHeight != 0 {
			return errors.New("storage proof was found")
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Test getParentOutputIDs
func TestWatchdogGetParents(t *testing.T) {
	// Create a txn set that is a long chain of transactions.
	rootTx := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{
			{
				ParentID: types.SiacoinOutputID(crypto.HashObject(fastrand.Bytes(32))),
			},
		},
		SiacoinOutputs: []types.SiacoinOutput{
			{
				UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32))),
			},
		},
	}
	txnSet := []types.Transaction{rootTx}
	for i := 0; i < 10; i++ {
		txnSet = addChildren(txnSet, 1, true)
	}
	// Verify that this is a complete chain.
	for i := 0; i < 10-1; i++ {
		parentID := txnSet[i].SiacoinInputs[0].ParentID
		expectedID := txnSet[i+1].SiacoinOutputID(0)
		if parentID != expectedID {
			t.Fatal("Invalid txn chain", i, parentID, expectedID)
		}
	}
	// Check that there is only one parent output, the parent of rootTx.
	parentOids := getParentOutputIDs(txnSet)
	if len(parentOids) != 1 {
		t.Fatal("expected exactly one parent output", len(parentOids))
	}
	if parentOids[0] != rootTx.SiacoinInputs[0].ParentID {
		t.Fatal("Wrong parent output id found")
	}

	rootTx2 := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{
			{
				ParentID: types.SiacoinOutputID(crypto.HashObject(fastrand.Bytes(32))),
			},
			{
				ParentID: types.SiacoinOutputID(crypto.HashObject(fastrand.Bytes(32))),
			},
		},
	}
	txnSet2 := []types.Transaction{rootTx2}
	// Make a transaction tree.
	for i := 0; i < 10; i++ {
		txnSet2 = addChildren(txnSet2, i*2, false)
	}
	parentOids2 := getParentOutputIDs(txnSet2)
	if len(parentOids2) != 2 {
		t.Fatal("expected exactly one parent output", len(parentOids2))
	}
	for i := 0; i < len(rootTx2.SiacoinInputs); i++ {
		foundParent := false
		for j := 0; j < len(parentOids2); j++ {
			if parentOids2[i] == rootTx2.SiacoinInputs[j].ParentID {
				foundParent = true
			}
		}
		if !foundParent {
			t.Fatal("didn't find parent output")
		}
	}

	// Create a txn set with A LOT of root transactions/outputs.
	numBigRoots := 100
	bigRoots := make([]types.Transaction, 0, numBigRoots)
	bigRootIDs := make(map[types.SiacoinOutputID]bool)

	// All the root txs create outputs spent in subRootTx.
	subRootTx := types.Transaction{
		SiacoinInputs: make([]types.SiacoinInput, numBigRoots),
	}

	for i := 0; i < numBigRoots; i++ {
		nextRootTx := types.Transaction{
			SiacoinInputs: []types.SiacoinInput{
				{
					ParentID: types.SiacoinOutputID(crypto.HashObject(fastrand.Bytes(32))),
				},
			},
			SiacoinOutputs: []types.SiacoinOutput{
				{
					UnlockHash: types.UnlockHash(crypto.HashObject(fastrand.Bytes(32))),
				},
			},
		}

		subRootTx.SiacoinInputs[i] = types.SiacoinInput{
			ParentID: nextRootTx.SiacoinOutputID(0),
		}
		bigRoots = append(bigRoots, nextRootTx)
		bigRootIDs[nextRootTx.SiacoinInputs[0].ParentID] = true
	}

	txnSet3 := append([]types.Transaction{subRootTx}, bigRoots...)
	parentOids3 := getParentOutputIDs(txnSet3)
	if len(parentOids3) != numBigRoots {
		t.Fatal("Wrong number of parent output ids", len(parentOids3))
	}

	for _, outputID := range parentOids3 {
		if !bigRootIDs[outputID] {
			t.Fatal("parent ID is not a root !", outputID)
		}
	}

	// Now create a bunch of transactions below subRootTx. This should NOT change the parentOids.
	chainLength := 12
	for i := 0; i < chainLength; i++ {
		txnSet3 = addChildren(txnSet3, i, false)
	}
	parentOids4 := getParentOutputIDs(txnSet3)
	if len(parentOids4) != numBigRoots {
		t.Fatal("Wrong number of parent output ids", len(parentOids4))
	}
	for _, outputID := range parentOids4 {
		if !bigRootIDs[outputID] {
			t.Fatal("parent ID is not a root !", outputID)
		}
	}
}

// TestWatchdogPruning tests watchdog pruning of formation transction sets by
// creating a large dependency set for the file contract transaction.
func TestWatchdogPruning(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a txn set with A LOT of root transactions/outputs.
	numRoots := 10
	chainLength := 12
	txnSet, roots, subRootTx, fcTxn, rootParents := createTestTransactionTree(numRoots, chainLength)
	fcID := fcTxn.FileContractID(0)

	// create testing trio
	_, c, _, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	revisionTxn := createFakeRevisionTxn(fcID, 1, 10000, 10005)

	// Give the watchdog a gated transaction pool. This precents it from
	// rejecting the fake formation transaction set.
	gatedTpool := gatedTpool{
		tpoolGate: &tpoolGate{gateClosed: true,
			logTxns: true,
			txnSets: make([][]types.Transaction, 0, 0),
		},
		transactionPool: c.tpool,
	}
	c.staticWatchdog.mu.Lock()
	c.staticWatchdog.tpool = gatedTpool
	c.staticWatchdog.mu.Unlock()

	// Signal the watchdog with this file contract and formation transaction.
	monitorContractArgs := monitorContractArgs{
		false,
		fcID,
		revisionTxn,
		txnSet,
		txnSet[0],
		nil,
		5000,
	}
	err = c.staticWatchdog.callMonitorContract(monitorContractArgs)
	if err != nil {
		t.Fatal(err)
	}

	c.staticWatchdog.mu.Lock()
	updatedTxnSet := c.staticWatchdog.contracts[fcID].formationTxnSet
	dependencies := getParentOutputIDs(updatedTxnSet)
	c.staticWatchdog.mu.Unlock()

	originalSetLen := len(updatedTxnSet)

	if len(dependencies) != numRoots {
		t.Fatal("Expected different number of dependencies", len(dependencies), numRoots)
	}

	// "mine" the root transactions in one at a time.
	for i := 0; i < numRoots; i++ {
		block := types.Block{
			Transactions: []types.Transaction{
				roots[i],
			},
		}

		newBlockCC := modules.ConsensusChange{
			AppliedBlocks: []types.Block{block},
		}
		c.staticWatchdog.callScanConsensusChange(newBlockCC)

		// The number of dependencies should be going down
		c.staticWatchdog.mu.Lock()
		updatedTxnSet = c.staticWatchdog.contracts[fcID].formationTxnSet
		dependencies = getParentOutputIDs(updatedTxnSet)
		c.staticWatchdog.mu.Unlock()

		// The number of dependencies shouldn't change, but the number of
		// transactions in the set should be decreasing.
		if len(dependencies) != numRoots {
			t.Fatal("Expected num of dependencies to stay the same")
		}
		if len(updatedTxnSet) != originalSetLen-i-1 {
			t.Fatal("unexpected txn set length", len(updatedTxnSet), i, originalSetLen-i)
		}
	}
	// Mine the subRootTx. The number of dependencies should go down to just 1
	// now.
	block := types.Block{
		Transactions: []types.Transaction{
			subRootTx,
		},
	}
	newBlockCC := modules.ConsensusChange{
		AppliedBlocks: []types.Block{block},
	}
	c.staticWatchdog.callScanConsensusChange(newBlockCC)

	// The number of dependencies should be going down
	c.staticWatchdog.mu.Lock()
	updatedTxnSet = c.staticWatchdog.contracts[fcID].formationTxnSet
	dependencies = getParentOutputIDs(updatedTxnSet)
	c.staticWatchdog.mu.Unlock()

	// Check that all the root transactions are gone.
	for i := 0; i < numRoots; i++ {
		rootTxID := roots[i].ID()
		for j := 0; j < len(updatedTxnSet); j++ {
			if updatedTxnSet[j].ID() == rootTxID {
				t.Fatal("Expected root to be gone")
			}
		}
	}
	// Check that the subRootTx is gone now.
	for _, txn := range updatedTxnSet {
		if txn.ID() == subRootTx.ID() {
			t.Fatal("subroot tx still present")
		}
	}

	// The number of dependencies shouldn't change, but the number of
	// transactions in the set should be decreasing.
	if len(dependencies) != 1 {
		t.Fatal("Expected num of dependencies to go down", len(dependencies))
	}
	if rootParents[dependencies[0]] {
		t.Fatal("the remaining dependency should not be a root output")
	}
	if len(updatedTxnSet) != originalSetLen-numRoots-1 {
		t.Fatal("unexpected txn set length", len(updatedTxnSet), originalSetLen-numRoots)
	}

	c.mu.Lock()
	_, doubleSpendDetected := c.doubleSpentContracts[fcID]
	c.mu.Unlock()
	if doubleSpendDetected {
		t.Fatal("non-existent double spend found!")
	}
}

func TestWatchdogDependencyAdding(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a txn set with 10 root transactions/outputs.
	numRoots := 10
	chainLength := 12
	txnSet, _, subRootTx, fcTxn, rootParents := createTestTransactionTree(numRoots, chainLength)
	fcID := fcTxn.FileContractID(0)

	// create testing trio
	_, c, _, err := newTestingTrio(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	revisionTxn := createFakeRevisionTxn(fcID, 1, 10000, 10005)
	formationSet := []types.Transaction{fcTxn}

	// Signal the watchdog with this file contract and formation transaction.
	monitorContractArgs := monitorContractArgs{
		false,
		fcID,
		revisionTxn,
		formationSet,
		txnSet[0],
		nil,
		5000,
	}
	err = c.staticWatchdog.callMonitorContract(monitorContractArgs)
	if err != nil {
		t.Fatal(err)
	}

	c.staticWatchdog.mu.Lock()
	_, inWatchdog := c.staticWatchdog.contracts[fcID]
	if !inWatchdog {
		t.Fatal("Expected watchdog to be aware of contract", fcID)
	}
	updatedTxnSet := c.staticWatchdog.contracts[fcID].formationTxnSet
	outputDependencies := getParentOutputIDs(updatedTxnSet)
	c.staticWatchdog.mu.Unlock()

	if len(outputDependencies) != 1 {
		t.Fatal("Expected different number of outputDependencies", len(outputDependencies))
	}

	// Revert the chain transactions in 1 block.
	txnLength := len(txnSet) - numRoots - 1
	block1 := types.Block{
		Transactions: txnSet[1:txnLength],
	}
	revertCC1 := modules.ConsensusChange{
		RevertedBlocks: []types.Block{block1},
	}
	c.staticWatchdog.callScanConsensusChange(revertCC1)

	// The number of outputDependencies should be going up
	c.staticWatchdog.mu.Lock()
	updatedTxnSet = c.staticWatchdog.contracts[fcID].formationTxnSet
	outputDependencies = getParentOutputIDs(updatedTxnSet)
	c.staticWatchdog.mu.Unlock()
	// The number of outputDependencies shouldn't change, but the number of
	// transactions in the set should be increasing.
	if len(outputDependencies) != 1 {
		t.Fatal("Expected num of outputDependencies to stay the same", len(outputDependencies))
	}
	if len(updatedTxnSet) != len(block1.Transactions)+1 {
		t.Fatal("unexpected txn set length", len(updatedTxnSet), len(block1.Transactions)+1)
	}
	// Revert the subRootTx. The number of outputDependencies should go up now.
	// now.
	block := types.Block{
		Transactions: []types.Transaction{
			subRootTx,
		},
	}
	revertCC := modules.ConsensusChange{
		RevertedBlocks: []types.Block{block},
	}
	c.staticWatchdog.callScanConsensusChange(revertCC)

	c.staticWatchdog.mu.Lock()
	contractData, ok := c.staticWatchdog.contracts[fcID]
	if !ok {
		t.Fatal("Expected watchdog to have contract")
	}
	updatedTxnSet = contractData.formationTxnSet
	outputDependencies = getParentOutputIDs(updatedTxnSet)
	found := contractData.contractFound
	c.staticWatchdog.mu.Unlock()

	if found {
		t.Fatal("contract should not have been found already")
	}

	// Sanity check: make sure the subRootTx is actually found in the set.
	foundSubRoot := false
	for i := 0; i < len(updatedTxnSet); i++ {
		if updatedTxnSet[i].ID() == subRootTx.ID() {
			foundSubRoot = true
			break
		}
	}
	if !foundSubRoot {
		t.Fatal("Subroot transaction should now be a dependency", len(updatedTxnSet))
	}

	// The number of outputDependencies shouldn't change, but the number of
	// transactions in the set should be decreasing.
	if len(outputDependencies) != numRoots {
		t.Fatal("Expected num of outputDependencies to go down", len(outputDependencies))
	}
	if rootParents[outputDependencies[0]] {
		t.Fatal("the remaining dependency should not be a root output")
	}

	// All transactions except for the roots should be dependency transactions.
	if len(updatedTxnSet) != len(txnSet)-numRoots {
		t.Fatal("unexpected txn set length", len(updatedTxnSet), len(txnSet)-numRoots)
	}
}
