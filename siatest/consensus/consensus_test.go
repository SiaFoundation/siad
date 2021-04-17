package consensus

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node"
	"go.sia.tech/siad/siatest"
	"go.sia.tech/siad/types"
)

// TestApiHeight checks if the consensus api endpoint works
func TestApiHeight(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testDir := consensusTestDir(t.Name())

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Send GET request
	cg, err := testNode.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	height := cg.Height

	// Mine a block
	if err := testNode.MineBlock(); err != nil {
		t.Fatal(err)
	}

	// Request height again and check if it increased
	cg, err = testNode.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	if cg.Height != height+1 {
		t.Fatal("Height should have increased by 1 block")
	}
}

// TestConsensusBlocksIDGet tests the /consensus/blocks endpoint
func TestConsensusBlocksIDGet(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Create a testgroup
	groupParams := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(consensusTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	testNode := tg.Miners()[0]

	// Send /consensus request
	endBlock, err := testNode.ConsensusGet()
	if err != nil {
		t.Fatal("Failed to call ConsensusGet():", err)
	}

	// Loop over blocks and compare
	var i types.BlockHeight
	var zeroID types.BlockID
	for i = 0; i <= endBlock.Height; i++ {
		cbhg, err := testNode.ConsensusBlocksHeightGet(i)
		if err != nil {
			t.Fatal("Failed to retrieve block by height:", err)
		}
		cbig, err := testNode.ConsensusBlocksIDGet(cbhg.ID)
		if err != nil {
			t.Fatal("Failed to retrieve block by ID:", err)
		}
		// Confirm blocks received by both endpoints are the same
		if !reflect.DeepEqual(cbhg, cbig) {
			t.Fatal("Blocks not equal")
		}
		// Confirm Fields were set properly
		// Ignore ParentID and MinerPayouts for genisis block
		if cbig.ParentID == zeroID && i != 0 {
			t.Fatal("ParentID wasn't set correctly")
		}
		if len(cbig.MinerPayouts) == 0 && i != 0 {
			t.Fatal("Block has no miner payouts")
		}
		if cbig.Timestamp == types.Timestamp(0) {
			t.Fatal("Timestamp wasn't set correctly")
		}
		if len(cbig.Transactions) == 0 {
			t.Fatal("Block doesn't have any transactions even though it should")
		}

		// Verify IDs
		for _, tx := range cbhg.Transactions {
			// Building transaction of type Transaction to use as
			// comparison for ID creation
			txn := types.Transaction{
				SiacoinInputs:         tx.SiacoinInputs,
				FileContractRevisions: tx.FileContractRevisions,
				StorageProofs:         tx.StorageProofs,
				SiafundInputs:         tx.SiafundInputs,
				MinerFees:             tx.MinerFees,
				ArbitraryData:         tx.ArbitraryData,
				TransactionSignatures: tx.TransactionSignatures,
			}
			for _, sco := range tx.SiacoinOutputs {
				txn.SiacoinOutputs = append(txn.SiacoinOutputs, types.SiacoinOutput{
					Value:      sco.Value,
					UnlockHash: sco.UnlockHash,
				})
			}
			for i, fc := range tx.FileContracts {
				txn.FileContracts = append(txn.FileContracts, types.FileContract{
					FileSize:       fc.FileSize,
					FileMerkleRoot: fc.FileMerkleRoot,
					WindowStart:    fc.WindowStart,
					WindowEnd:      fc.WindowEnd,
					Payout:         fc.Payout,
					UnlockHash:     fc.UnlockHash,
					RevisionNumber: fc.RevisionNumber,
				})
				for _, vp := range fc.ValidProofOutputs {
					txn.FileContracts[i].ValidProofOutputs = append(txn.FileContracts[i].ValidProofOutputs, types.SiacoinOutput{
						Value:      vp.Value,
						UnlockHash: vp.UnlockHash,
					})
				}
				for _, mp := range fc.MissedProofOutputs {
					txn.FileContracts[i].MissedProofOutputs = append(txn.FileContracts[i].MissedProofOutputs, types.SiacoinOutput{
						Value:      mp.Value,
						UnlockHash: mp.UnlockHash,
					})
				}
			}
			for _, sfo := range tx.SiafundOutputs {
				txn.SiafundOutputs = append(txn.SiafundOutputs, types.SiafundOutput{
					Value:      sfo.Value,
					UnlockHash: sfo.UnlockHash,
					ClaimStart: types.ZeroCurrency,
				})
			}

			// Verify SiacoinOutput IDs
			for i, sco := range tx.SiacoinOutputs {
				if sco.ID != txn.SiacoinOutputID(uint64(i)) {
					t.Fatalf("SiacoinOutputID not as expected, got %v expected %v", sco.ID, txn.SiacoinOutputID(uint64(i)))
				}
			}

			// FileContracts
			for i, fc := range tx.FileContracts {
				// Verify FileContract ID
				fcid := txn.FileContractID(uint64(i))
				if fc.ID != fcid {
					t.Fatalf("FileContract ID not as expected, got %v expected %v", fc.ID, fcid)
				}
				// Verify ValidProof IDs
				for j, vp := range fc.ValidProofOutputs {
					if vp.ID != fcid.StorageProofOutputID(types.ProofValid, uint64(j)) {
						t.Fatalf("File Contract ValidProofOutputID not as expected, got %v expected %v", vp.ID, fcid.StorageProofOutputID(types.ProofValid, uint64(j)))
					}
				}
				// Verify MissedProof IDs
				for j, mp := range fc.MissedProofOutputs {
					if mp.ID != fcid.StorageProofOutputID(types.ProofMissed, uint64(j)) {
						t.Fatalf("File Contract MissedProofOutputID not as expected, got %v expected %v", mp.ID, fcid.StorageProofOutputID(types.ProofMissed, uint64(j)))
					}
				}
			}

			// Verify SiafundOutput IDs
			for i, sfo := range tx.SiafundOutputs {
				// Failing, switch back to !=
				if sfo.ID != txn.SiafundOutputID(uint64(i)) {
					t.Fatalf("SiafundOutputID not as expected, got %v expected %v", sfo.ID, txn.SiafundOutputID(uint64(i)))
				}
			}
		}
	}
}

type testSubscriber struct {
	height types.BlockHeight
	ccid   modules.ConsensusChangeID
	mu     sync.Mutex
}

func (ts *testSubscriber) ProcessConsensusChange(cc modules.ConsensusChange) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.height += types.BlockHeight(len(cc.AppliedBlocks))
	ts.height -= types.BlockHeight(len(cc.RevertedBlocks))
	ts.ccid = cc.ID
}

// TestConsensusSubscribe tests the /consensus/subscribe endpoint
func TestConsensusSubscribe(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Create a testgroup
	groupParams := siatest.GroupParams{
		Miners: 1,
	}
	tg, err := siatest.NewGroupFromTemplate(consensusTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	testNode := tg.Miners()[0]

	// subscribe via api, but cancel immediately
	s := &testSubscriber{height: ^types.BlockHeight(0)}
	cancel := make(chan struct{})
	close(cancel)
	errCh, _ := testNode.ConsensusSetSubscribe(s, modules.ConsensusChangeBeginning, cancel)
	if err := <-errCh; errors.Is(err, context.Canceled) {
		t.Fatal("expected context.Canceled, got", err)
	}
	// subscribe again without cancelling
	errCh, unsubscribe := testNode.ConsensusSetSubscribe(s, modules.ConsensusChangeBeginning, nil)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}

	// subscriber should be synced with miner
	cg, err := testNode.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	if s.height != cg.Height {
		t.Fatal("subscriber not synced", s.height, cg.Height)
	}

	// unsubscribe and mine more blocks; subscriber should not see them
	unsubscribe()
	for i := 0; i < 5; i++ {
		err = testNode.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}
	cg, err = testNode.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	if s.height == cg.Height {
		t.Fatal("subscriber was not unsubscribed", s.height, cg.Height)
	}

	// resubscribe from most recent ccid; should resync
	errCh, unsubscribe = testNode.ConsensusSetSubscribe(s, s.ccid, nil)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	if s.height != cg.Height {
		t.Fatal("subscriber not synced", s.height, cg.Height)
	}
}

// TestFoundationHardfork tests the foundation hardfork, ensuring that upgraded
// nodes have the ability to follow the hardfork, and ensuring that the
// mechanisms for spending the foundation coins are functional.
func TestFoundationHardfork(t *testing.T) {
	if types.FoundationSubsidyFrequency < types.MaturityDelay+2 {
		t.Fatal("Bad constants: subsidy freqency needs to be 2 greater than maturity delay")
	}
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a testgroup. Include hosts and renters to check that basic renting
	// functions continue to work after the fork.
	groupParams := siatest.GroupParams{
		Hosts:   2,
		Miners:  1,
		Renters: 1,
	}
	tg, err := siatest.NewGroupFromTemplate(consensusTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	ws, err := tg.AddNodes(node.WalletTemplate)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatal("bad")
	}
	w := ws[0]
	r := tg.Renters()[0]
	m := tg.Miners()[0]

	// Have the renter upload some files to Sia prior to the foundation fork
	// activating. We will check at the end of the test whether these files are
	// still retrievable, indicating that upgraded renters and hosts had no
	// trouble following along in the fork.
	localFile, remoteFile, err := r.UploadNewFileBlocking(100+siatest.Fuzz(), 1, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	localFileData, err := localFile.Data()
	if err != nil {
		t.Fatal(err)
	}
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}

	// Check that the height is still pre-hardfork at this point. If it's not,
	// we'll need to adjust the constant that sets when the hardfork activates.
	height, err := w.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	if height >= types.FoundationHardforkHeight {
		t.Log(height)
		t.Log(types.FoundationHardforkHeight)
		t.Fatal("test has already passed the foundation hardfork height, test is invalid")
	}

	// mineBlock is a helper to mine a block using the miner.
	mineBlock := func() {
		err := m.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
		err = tg.Sync()
		if err != nil {
			t.Fatal(err)
		}
		// The renter and host need to run contract maintenance in the
		// background, if we mine blocks too quickly we may reach the point
		// where the contracts have expired before the renter and host have had
		// time to run the renewal protocol. By waiting a full second after each
		// block, we ensure that there is plenty of time for renewals to happen.
		time.Sleep(time.Second)
	}

	// waitForTxns waits until the miner tpool has the specified number of
	// transactions in it. Use this when sending transactions from one node to
	// another to ensure that they get confirmed.
	waitForTxns := func(numTransactions int) {
		err := build.Retry(100, 100*time.Millisecond, func() error {
			tptg, err := m.TransactionPoolTransactionsGet()
			if err != nil {
				return err
			}
			if len(tptg.Transactions) != numTransactions {
				return errors.New("wrong number of txns")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	// submitTxnToMiner will submit a transaction to the tpool of the miner.
	submitTxnToMiner := func(txn types.Transaction) {
		err := m.TransactionPoolRawPost(txn, nil)
		if err != nil {
			t.Fatal(err)
		}
		tptg, err := m.TransactionPoolTransactionsGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(tptg.Transactions) != 1 {
			t.Fatal("wrong number of transactions", len(tptg.Transactions))
		}
	}

	// Create a transaction that updates the foundation addresses, to be
	// submitted to the blockchain prior to the fork. Because of how the
	// foundation code scans for updates, this will require sending money to the
	// foundation addresses prior to the hardfork activating.
	//
	// Generate the foundation primary and failsafe addresses and keys.
	foundationPrimaryUnlockConditions, foundationPrimaryKeys := types.GenerateDeterministicMultisig(2, 3, types.InitialFoundationTestingSalt)
	foundationFailsafeUnlockConditions, foundationFailsafeKeys := types.GenerateDeterministicMultisig(3, 5, types.InitialFoundationFailsafeTestingSalt)
	foundationPrimaryAddress := foundationPrimaryUnlockConditions.UnlockHash()
	foundationFailsafeAddress := foundationFailsafeUnlockConditions.UnlockHash()
	//
	// Send money to the foundation addresses from the wallet.
	balance, err := w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if balance.Cmp(types.SiacoinPrecision.Mul64(100)) < 0 {
		t.Log(balance)
		t.Fatal("balance is too low in the wallet")
	}
	//
	// Check that the miner tpool is empty before sending transactions.
	tptg, err := m.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal("too many transactions")
	}
	primaryWSP, err := w.WalletSiacoinsPost(types.SiacoinPrecision, foundationPrimaryAddress, false)
	if err != nil {
		t.Fatal(err)
	}
	failsafeWSP, err := w.WalletSiacoinsPost(types.SiacoinPrecision, foundationFailsafeAddress, false)
	if err != nil {
		t.Fatal(err)
	}
	failsafeWSP2, err := w.WalletSiacoinsPost(types.SiacoinPrecision, foundationFailsafeAddress, false)
	if err != nil {
		t.Fatal(err)
	}
	//
	// Grab the output ids for the siacoin outputs that were sent to the
	// foundation addresses. These will be needed to create the transaction that
	// updates the subsidy ownership.
	var primaryOutputID types.SiacoinOutputID
	var failsafeOutputID types.SiacoinOutputID
	var failsafeOutputID2 types.SiacoinOutputID
	found := false
	for _, txn := range primaryWSP.Transactions {
		for i, output := range txn.SiacoinOutputs {
			if output.UnlockHash == foundationPrimaryAddress {
				found = true
				primaryOutputID = txn.SiacoinOutputID(uint64(i))
			}
		}
	}
	if !found {
		t.Fatal("didn't find output id")
	}
	found = false
	for _, txn := range failsafeWSP.Transactions {
		for i, output := range txn.SiacoinOutputs {
			if output.UnlockHash == foundationFailsafeAddress {
				found = true
				failsafeOutputID = txn.SiacoinOutputID(uint64(i))
			}
		}
	}
	if !found {
		t.Fatal("didn't find output ids")
	}
	found = false
	for _, txn := range failsafeWSP2.Transactions {
		for i, output := range txn.SiacoinOutputs {
			if output.UnlockHash == foundationFailsafeAddress {
				found = true
				failsafeOutputID2 = txn.SiacoinOutputID(uint64(i))
			}
		}
	}
	if !found {
		t.Fatal("didn't find output ids")
	}
	if primaryOutputID == failsafeOutputID {
		t.Fatal("setup is incorrect")
	}
	if failsafeOutputID2 == primaryOutputID {
		t.Fatal("setup is incorrect")
	}
	if failsafeOutputID2 == failsafeOutputID {
		t.Fatal("setup is incorrect")
	}
	//
	// Block until the miner has the transactions in its tpool, then mine a
	// block.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		tptg, err := m.TransactionPoolTransactionsGet()
		if err != nil {
			t.Fatal(err)
		}
		// Check that there is a transaction for both output ids.
		primaryFound := false
		failsafeFound := false
		failsafe2Found := false
		for _, txn := range tptg.Transactions {
			for _, output := range txn.SiacoinOutputs {
				if output.UnlockHash == foundationPrimaryAddress {
					primaryFound = true
					continue
				}
				if output.UnlockHash == foundationFailsafeAddress && !failsafeFound {
					failsafeFound = true
					continue
				}
				if output.UnlockHash == foundationFailsafeAddress && failsafeFound {
					failsafe2Found = true
					continue
				}
			}
		}
		if !primaryFound || !failsafeFound || !failsafe2Found {
			return errors.New("transactions are not in miner tpool")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mineBlock()
	//
	// Get the new height, which is needed to sign transactions.
	height, err = w.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	//
	// Create the transaction that attempts to update the foundation addresses
	// using the primary address.
	updateWithPrimaryTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         primaryOutputID,
			UnlockConditions: foundationPrimaryUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Value: types.SiacoinPrecision},
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{1, 2},
			NewFailsafe: types.UnlockHash{3, 4},
		})},
		TransactionSignatures: make([]types.TransactionSignature, foundationPrimaryUnlockConditions.SignaturesRequired),
	}
	for i := range updateWithPrimaryTxn.TransactionSignatures {
		updateWithPrimaryTxn.TransactionSignatures[i].ParentID = crypto.Hash(primaryOutputID)
		updateWithPrimaryTxn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		updateWithPrimaryTxn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(updateWithPrimaryTxn.SigHash(i, height), foundationPrimaryKeys[i])
		updateWithPrimaryTxn.TransactionSignatures[i].Signature = sig[:]
	}
	//
	// Create the transaction that attempts to update the foundation addresses
	// using the failsafe address.
	updateWithFailsafeTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         failsafeOutputID,
			UnlockConditions: foundationFailsafeUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Value: types.SiacoinPrecision},
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{5, 6},
			NewFailsafe: types.UnlockHash{7, 8},
		})},
		TransactionSignatures: make([]types.TransactionSignature, foundationFailsafeUnlockConditions.SignaturesRequired),
	}
	for i := range updateWithFailsafeTxn.TransactionSignatures {
		updateWithFailsafeTxn.TransactionSignatures[i].ParentID = crypto.Hash(failsafeOutputID)
		updateWithFailsafeTxn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		updateWithFailsafeTxn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(updateWithFailsafeTxn.SigHash(i, height), foundationFailsafeKeys[i])
		updateWithFailsafeTxn.TransactionSignatures[i].Signature = sig[:]
	}
	//
	// Submit the update transactions to the miner tpool and mine them into a
	// block.
	err = m.TransactionPoolRawPost(updateWithPrimaryTxn, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = m.TransactionPoolRawPost(updateWithFailsafeTxn, nil)
	if err != nil {
		t.Fatal(err)
	}
	tptg, err = m.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 2 {
		t.Fatal("wrong number of transactions", len(tptg.Transactions))
	}
	mineBlock()
	//
	// Check that we are still below the foundation hardfork height.
	height, err = w.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	if height >= types.FoundationHardforkHeight {
		t.Fatal("foundation fork height passed already")
	}

	// Mine until after the hardfork.
	mineToHeight := types.FoundationHardforkHeight + 1 + types.MaturityDelay
	for i := height; i <= mineToHeight; i++ {
		mineBlock()
	}
	// Check that the foundation addresses match the original.
	cg, err := m.ConsensusGet()
	if cg.FoundationPrimaryUnlockHash != foundationPrimaryAddress {
		t.Fatal("foundation unlock hash changed by a pre-hardfork txn")
	}
	if cg.FoundationFailsafeUnlockHash != foundationFailsafeAddress {
		t.Fatal("foundation failsafe changed by a pre-hardfork txn")
	}

	// Create a transaction that spends the initial foundation subsidy to a
	// wallet.
	//
	// Determine the id of the subsidy output.
	cbhg, err := m.ConsensusBlocksHeightGet(types.FoundationHardforkHeight)
	if err != nil {
		t.Fatal(err)
	}
	initialSubsidyID := cbhg.ID.FoundationSubsidyID()
	//
	// Fetch an address from the wallet.
	wag, err := w.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	addr := wag.Address
	//
	// Create the transaction that spends the foundation subsidy to the wallet
	// addr.
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         initialSubsidyID,
			UnlockConditions: foundationPrimaryUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      types.InitialFoundationSubsidy.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		TransactionSignatures: make([]types.TransactionSignature, foundationPrimaryUnlockConditions.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(initialSubsidyID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, types.FoundationHardforkHeight+1), foundationPrimaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	err = txn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	//
	// Submit the update transactions to the miner tpool.
	submitTxnToMiner(txn)
	mineBlock()
	//
	// Verify that the coins make it to the wallet.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		balance, err := w.ConfirmedBalance()
		if err != nil {
			return err
		}
		if balance.Cmp(types.InitialFoundationSubsidy) < 0 {
			return errors.New("wallet does not seem to possess the foundation subsidy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	//
	// Verify that the wallet can send those coins like any other coins.
	wag, err = m.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.WalletSiacoinsPost(types.InitialFoundationSubsidy, wag.Address, false)
	if err != nil {
		t.Fatal(err)
	}
	waitForTxns(2)
	//
	// Mine the transactions into a block.
	err = m.MineBlock()
	if err != nil {
		t.Fatal(err)
	}
	err = tg.Sync()
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // give some time for contract maintenance
	//
	// Verfiy that the wallet sent the coins.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		balance, err := w.ConfirmedBalance()
		if err != nil {
			return err
		}
		if balance.Cmp(types.InitialFoundationSubsidy) >= 0 {
			return errors.New("wallet does not seem to possess the foundation subsidy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine until the first monthy output is created. Then create a transaction
	// that spends that monthly output and verify that the monthly outputs are
	// usable like any other outputs.
	height, err = m.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	monthOneHeight := types.FoundationHardforkHeight + types.FoundationSubsidyFrequency
	mineToHeight = monthOneHeight + types.MaturityDelay
	for i := height; i <= mineToHeight; i++ {
		mineBlock()
	}
	//
	// Determine the id of the subsidy output.
	cbhg, err = m.ConsensusBlocksHeightGet(monthOneHeight)
	if err != nil {
		t.Fatal(err)
	}
	monthOneSubsidyID := cbhg.ID.FoundationSubsidyID()
	//
	// Fetch an address from the wallet.
	wag, err = w.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	addr = wag.Address
	//
	// Create the transaction that spends the foundation subsidy to the wallet
	// addr.
	subsidyCoins := types.FoundationSubsidyPerBlock.Mul64(uint64(types.FoundationSubsidyFrequency))
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthOneSubsidyID,
			UnlockConditions: foundationPrimaryUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		TransactionSignatures: make([]types.TransactionSignature, foundationPrimaryUnlockConditions.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(monthOneSubsidyID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, mineToHeight), foundationPrimaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	err = txn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	// Get the original balance of the wallet.
	originalBalance, err := w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	submitTxnToMiner(txn)
	mineBlock()
	//
	// Verify that the coins make it to the wallet.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		balance, err := w.ConfirmedBalance()
		if err != nil {
			return err
		}
		expectedBalance := originalBalance.Add(subsidyCoins).Sub(types.SiacoinPrecision)
		if balance.Cmp(expectedBalance) < 0 {
			return errors.New("wallet does not seem to possess the foundation subsidy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	//
	// Verify that the wallet can send those coins like any other coins.
	//
	// NOTE: we drain the entire balance to ensure that the outputs we care
	// about are used.
	balance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	wag, err = m.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.WalletSiacoinsPost(balance.Sub(types.SiacoinPrecision), wag.Address, false)
	if err != nil {
		t.Fatal(err)
	}
	waitForTxns(2)
	if err != nil {
		t.Fatal(err)
	}
	mineBlock()
	//
	// Verfiy that the wallet sent the coins.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		balance, err := w.ConfirmedBalance()
		if err != nil {
			return err
		}
		if balance.Cmp(types.SiacoinPrecision) >= 0 {
			return errors.New("wallet does not seem to have sent the foundation subsidy")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mine until the second monthly output is created. Then create a
	// transaction that spends the subsidy while also changing the foundation
	// addresses using the current primary address.
	//
	// Mine until the second monthly subsidy has been allocated.
	height, err = m.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	monthTwoHeight := types.FoundationHardforkHeight + (types.FoundationSubsidyFrequency * 2)
	mineToHeight = monthTwoHeight + types.MaturityDelay
	for i := height; i <= mineToHeight; i++ {
		mineBlock()
	}
	//
	// Determine the id of the subsidy output.
	cbhg, err = m.ConsensusBlocksHeightGet(monthTwoHeight)
	if err != nil {
		t.Fatal(err)
	}
	monthTwoSubsidyID := cbhg.ID.FoundationSubsidyID()
	//
	// Create the new keys for the new foundation addresses.
	newPrimaryUC, newPrimaryKeys := types.GenerateDeterministicMultisig(3, 4, "12")
	newPrimaryAddr := newPrimaryUC.UnlockHash()
	//
	// Create the transaction that attempts to rotate the old signing keys,
	// while simultaneously spending the month two subsidy output.
	monthTwoTxn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthTwoSubsidyID,
			UnlockConditions: foundationPrimaryUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  newPrimaryAddr,
			NewFailsafe: foundationFailsafeAddress,
		})},
		TransactionSignatures: make([]types.TransactionSignature, foundationPrimaryUnlockConditions.SignaturesRequired),
	}
	for i := range monthTwoTxn.TransactionSignatures {
		monthTwoTxn.TransactionSignatures[i].ParentID = crypto.Hash(monthTwoSubsidyID)
		monthTwoTxn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		monthTwoTxn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(monthTwoTxn.SigHash(i, height), foundationPrimaryKeys[i])
		monthTwoTxn.TransactionSignatures[i].Signature = sig[:]
	}
	err = monthTwoTxn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	//
	// Get the old balance of the wallet.
	balance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	submitTxnToMiner(monthTwoTxn)
	mineBlock()
	//
	// Verify that the subsidy is now in the wallet.
	newBalance, err := w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if newBalance.Cmp(balance.Add(subsidyCoins).Sub(types.SiacoinPrecision)) != 0 {
		t.Fatal("unexpected balance")
	}

	// Mine until the third monthly output is created. Try to spend the third
	// monthly output using the old foundation address. Ensure it fails. Then
	// try to spend the third monthly output using the updated foundation
	// address. Ensure that it succeeds.
	//
	// Mine until the third monthly subsidy has been allocated.
	height, err = m.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	monthThreeHeight := types.FoundationHardforkHeight + (types.FoundationSubsidyFrequency * 3)
	mineToHeight = monthThreeHeight + types.MaturityDelay
	for i := height; i <= mineToHeight; i++ {
		mineBlock()
	}
	//
	// Determine the id of the subsidy output.
	cbhg, err = m.ConsensusBlocksHeightGet(monthThreeHeight)
	if err != nil {
		t.Fatal(err)
	}
	monthThreeSubsidyID := cbhg.ID.FoundationSubsidyID()
	//
	// Create the transaction that attempts to spend the month three subsidy
	// with the old foundation primary address.
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthThreeSubsidyID,
			UnlockConditions: foundationPrimaryUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		TransactionSignatures: make([]types.TransactionSignature, foundationPrimaryUnlockConditions.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(primaryOutputID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, height), foundationPrimaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	//
	// Get the transaction onto the blockchain.
	err = m.TransactionPoolRawPost(txn, nil)
	if err == nil || !strings.Contains(err.Error(), types.ErrFrivolousSignature.Error()) {
		t.Fatal("the transaction is supposed to be rejected as invalid")
	}
	tptg, err = m.TransactionPoolTransactionsGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(tptg.Transactions) != 0 {
		t.Fatal("there should be no transactions in the tpool")
	}
	//
	// Actually spend month 3 of the subsidy with the rotated keys, and make
	// sure the rotation worked. At the same time, rotate the primary address to
	// the void, forcing a recovery using the failsafe.
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthThreeSubsidyID,
			UnlockConditions: newPrimaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{1},
			NewFailsafe: foundationFailsafeAddress,
		})},
		TransactionSignatures: make([]types.TransactionSignature, newPrimaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(monthThreeSubsidyID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, mineToHeight), newPrimaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	err = txn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	//
	// Get the old balance of the wallet.
	balance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	submitTxnToMiner(txn)
	mineBlock()
	//
	// Verify that the subsidy is now in the wallet.
	newBalance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if newBalance.Cmp(balance.Add(subsidyCoins).Sub(types.SiacoinPrecision)) != 0 {
		t.Fatal("unexpected balance")
	}

	// Create a transaction that attempts to use the failsafe to change the
	// foundation addresses before the timelock on the failsafe has expired.
	// This is done on an imaginary failsafe address with a timelock.
	monthFourHeight := types.FoundationHardforkHeight + (types.FoundationSubsidyFrequency * 4)
	timelockUC := foundationFailsafeUnlockConditions
	timelockUC.Timelock = monthFourHeight
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthThreeSubsidyID, // doesn't matter since this txn isn't going into consensus
			UnlockConditions: timelockUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{1},
			NewFailsafe: foundationFailsafeAddress,
		})},
		TransactionSignatures: make([]types.TransactionSignature, newPrimaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(monthThreeSubsidyID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, mineToHeight), foundationFailsafeKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	err = txn.StandaloneValid(mineToHeight)
	if err == nil || !strings.Contains(err.Error(), types.ErrTimelockNotSatisfied.Error()) {
		t.Fatal("there's supposed to be a timelock err here")
	}

	// Mine until the fourth monthly output is created. Because we set the
	// primary foundation address to the void in the previous transaction, the
	// new output will be unspendable. Create a transaction that changes the
	// foundation addresses using the failsafe address.
	height, err = m.BlockHeight()
	if err != nil {
		t.Fatal(height)
	}
	mineToHeight = monthFourHeight + types.MaturityDelay
	for i := height; i <= mineToHeight; i++ {
		mineBlock()
	}
	//
	// Create the transaction that using the failsafe to rotate the foundation
	// address.
	newPrimaryUC, newPrimaryKeys = types.GenerateDeterministicMultisig(4, 5, "45")
	newPrimaryAddr = newPrimaryUC.UnlockHash()
	newFailsafeUC, _ := types.GenerateDeterministicMultisig(5, 6, "56")
	newFailsafeAddr := newFailsafeUC.UnlockHash()
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         failsafeOutputID2,
			UnlockConditions: foundationFailsafeUnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{
			{Value: types.SiacoinPrecision},
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  newPrimaryAddr,
			NewFailsafe: newFailsafeAddr,
		})},
		TransactionSignatures: make([]types.TransactionSignature, foundationFailsafeUnlockConditions.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(failsafeOutputID2)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, height), foundationFailsafeKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	//
	// Mine the transaction into a block.
	err = txn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	submitTxnToMiner(txn)
	mineBlock()

	// Spend the fourth month subsidy using the new primary address, now that we
	// have reset the foundation addresses with the failsafe. Ensure that makes
	// into a wallet and can be spent from the wallet.
	cbhg, err = m.ConsensusBlocksHeightGet(monthFourHeight)
	if err != nil {
		t.Fatal(err)
	}
	monthFourSubsidyID := cbhg.ID.FoundationSubsidyID()
	txn = types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         monthFourSubsidyID,
			UnlockConditions: newPrimaryUC,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      subsidyCoins.Sub(types.SiacoinPrecision),
			UnlockHash: addr,
		}},
		MinerFees: []types.Currency{
			types.SiacoinPrecision,
		},
		ArbitraryData: [][]byte{encoding.MarshalAll(types.SpecifierFoundation, types.FoundationUnlockHashUpdate{
			NewPrimary:  types.UnlockHash{2},
			NewFailsafe: foundationFailsafeAddress,
		})},
		TransactionSignatures: make([]types.TransactionSignature, newPrimaryUC.SignaturesRequired),
	}
	for i := range txn.TransactionSignatures {
		txn.TransactionSignatures[i].ParentID = crypto.Hash(monthFourSubsidyID)
		txn.TransactionSignatures[i].CoveredFields = types.FullCoveredFields
		txn.TransactionSignatures[i].PublicKeyIndex = uint64(i)
		sig := crypto.SignHash(txn.SigHash(i, mineToHeight), newPrimaryKeys[i])
		txn.TransactionSignatures[i].Signature = sig[:]
	}
	err = txn.StandaloneValid(mineToHeight)
	if err != nil {
		t.Fatal(err)
	}
	//
	// Get the old balance of the wallet.
	balance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	submitTxnToMiner(txn)
	mineBlock()
	//
	// Verify that the subsidy is now in the wallet.
	newBalance, err = w.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if newBalance.Cmp(balance.Add(subsidyCoins).Sub(types.SiacoinPrecision)) != 0 {
		t.Fatal("unexpected balance")
	}

	// Check that the files uploaded before the hardfork activiation height are
	// still doing well, even after all of the paces that we have put the group
	// through with managing the foundation subsidy.
	_, remoteFileData, err := r.DownloadByStream(remoteFile)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(localFileData, remoteFileData) {
		t.Fatal(err)
	}
}
