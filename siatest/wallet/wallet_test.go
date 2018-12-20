package wallet

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestTransactionReorg makes sure that a processedTransaction isn't returned
// by the API after bein reverted.
func TestTransactionReorg(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create testing directory.
	testdir := walletTestDir(t.Name())

	// Create two miners
	miner1, err := siatest.NewNode(siatest.Miner(filepath.Join(testdir, "miner1")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := miner1.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	// miner1 sends a txn to itself and mines it.
	uc, err := miner1.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	wsp, err := miner1.WalletSiacoinsPost(types.SiacoinPrecision, uc.Address)
	if err != nil {
		t.Fatal(err)
	}
	blocks := 1
	for i := 0; i < blocks; i++ {
		if err := miner1.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	// wait until the transaction from before shows up as processed.
	txn := wsp.TransactionIDs[len(wsp.TransactionIDs)-1]
	err = build.Retry(100, 100*time.Millisecond, func() error {
		cg, err := miner1.ConsensusGet()
		if err != nil {
			return err
		}
		wtg, err := miner1.WalletTransactionsGet(1, cg.Height)
		if err != nil {
			return err
		}
		for _, t := range wtg.ConfirmedTransactions {
			if t.TransactionID == txn {
				return nil
			}
		}
		return errors.New("txn isn't processed yet")
	})
	if err != nil {
		t.Fatal(err)
	}
	miner2, err := siatest.NewNode(siatest.Miner(filepath.Join(testdir, "miner2")))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := miner2.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// miner2 mines 2 blocks now to create a longer chain than miner1.
	for i := 0; i < blocks+1; i++ {
		if err := miner2.MineBlock(); err != nil {
			t.Fatal(err)
		}
	}
	// miner1 and miner2 connect. This should cause a reorg that reverts the
	// transaction from before.
	if err := miner1.GatewayConnectPost(miner2.GatewayAddress()); err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		cg, err := miner1.ConsensusGet()
		if err != nil {
			return err
		}
		wtg, err := miner1.WalletTransactionsGet(1, cg.Height)
		if err != nil {
			return err
		}
		for _, t := range wtg.ConfirmedTransactions {
			if t.TransactionID == txn {
				return errors.New("txn is still processed")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestSignTransaction is a integration test for signing transaction offline
// using the API.
func TestSignTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(siatest.TestDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// get two outputs to spend
	unspentResp, err := testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs:", err)
	}
	outputs := unspentResp.Outputs
	wucg1, err := testNode.WalletUnlockConditionsGet(outputs[0].UnlockHash)
	if err != nil {
		t.Fatal("failed to get unlock conditions:", err)
	}
	wucg2, err := testNode.WalletUnlockConditionsGet(outputs[1].UnlockHash)
	if err != nil {
		t.Fatal("failed to get unlock conditions:", err)
	}

	// create a transaction that sends the outputs to the void, with no
	// signatures
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{
			{
				ParentID:         types.SiacoinOutputID(outputs[0].ID),
				UnlockConditions: wucg1.UnlockConditions,
			},
			{
				ParentID:         types.SiacoinOutputID(outputs[1].ID),
				UnlockConditions: wucg2.UnlockConditions,
			},
		},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      outputs[0].Value.Add(outputs[1].Value),
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: []types.TransactionSignature{
			{ParentID: crypto.Hash(outputs[0].ID), CoveredFields: types.CoveredFields{WholeTransaction: true}},
			{ParentID: crypto.Hash(outputs[1].ID), CoveredFields: types.CoveredFields{WholeTransaction: true}},
		},
	}

	// sign the first input
	signResp, err := testNode.WalletSignPost(txn, []crypto.Hash{txn.TransactionSignatures[0].ParentID})
	if err != nil {
		t.Fatal("failed to sign the transaction:", err)
	}
	txn = signResp.Transaction

	// txn should now have one signature
	if len(txn.TransactionSignatures[0].Signature) == 0 {
		t.Fatal("transaction was not signed")
	} else if len(txn.TransactionSignatures[1].Signature) != 0 {
		t.Fatal("second input was also signed")
	}

	// sign the second input
	signResp, err = testNode.WalletSignPost(txn, []crypto.Hash{txn.TransactionSignatures[1].ParentID})
	if err != nil {
		t.Fatal("failed to sign the transaction:", err)
	}
	txn = signResp.Transaction

	// txn should now have both signatures
	if len(txn.TransactionSignatures[0].Signature) == 0 || len(txn.TransactionSignatures[1].Signature) == 0 {
		t.Fatal("transaction was not signed")
	}

	// the resulting transaction should be valid; submit it to the tpool and
	// mine a block to confirm it
	if err := testNode.TransactionPoolRawPost(txn, nil); err != nil {
		t.Fatal("failed to add transaction to pool:", err)
	}
	if err := testNode.MineBlock(); err != nil {
		t.Fatal("failed to mine block", err)
	}

	// the wallet should no longer list the resulting output as spendable
	unspentResp, err = testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs")
	}
	for _, output := range unspentResp.Outputs {
		if output.ID == types.OutputID(txn.SiacoinInputs[0].ParentID) {
			t.Fatal("spent output still listed as spendable")
		}
	}
}

// TestWatchOnly tests the ability of the wallet to track addresses that it
// does not own.
func TestWatchOnly(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(siatest.TestDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// create an address manually and send coins to it
	sk, pk := crypto.GenerateKeyPair()
	uc := types.UnlockConditions{
		PublicKeys:         []types.SiaPublicKey{types.Ed25519PublicKey(pk)},
		SignaturesRequired: 1,
	}
	addr := uc.UnlockHash()

	_, err = testNode.WalletSiacoinsPost(types.SiacoinPrecision.Mul64(77), addr)
	if err != nil {
		t.Fatal(err)
	}
	testNode.MineBlock()

	// the output should not show up in UnspentOutputs, because the address is
	// not being tracked yet
	unspentResp, err := testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs:", err)
	} else if len(unspentResp.Outputs) == 0 {
		t.Fatal("expected at least one unspent output")
	}
	for _, o := range unspentResp.Outputs {
		if o.UnlockHash == addr {
			t.Fatal("shouldn't see addr in UnspentOutputs yet")
		}
		if o.IsWatchOnly {
			t.Error("no outputs should be marked watch-only yet")
		}
	}

	// track the address
	err = testNode.WalletWatchAddPost([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}

	// output should now show up
	unspentResp, err = testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs:", err)
	}
	var output modules.UnspentOutput
	for _, o := range unspentResp.Outputs {
		if o.UnlockHash == addr {
			output = o
			break
		}
	}
	if output.ID == (types.OutputID{}) {
		t.Fatal("addr not present in UnspentOutputs after WatchAddresses")
	}
	if !output.IsWatchOnly {
		t.Error("output should be marked watch-only")
	}

	// create a transaction that sends an output to the void
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         types.SiacoinOutputID(output.ID),
			UnlockConditions: uc,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      output.Value,
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID:      crypto.Hash(output.ID),
			CoveredFields: types.CoveredFields{WholeTransaction: true},
		}},
	}

	// sign the transaction
	cg, err := testNode.ConsensusGet()
	if err != nil {
		t.Fatal(err)
	}
	sig := crypto.SignHash(txn.SigHash(0, cg.Height), sk)
	txn.TransactionSignatures[0].Signature = sig[:]

	// the resulting transaction should be valid; submit it to the tpool and
	// mine a block to confirm it
	err = testNode.TransactionPoolRawPost(txn, nil)
	if err != nil {
		t.Fatal(err)
	}
	testNode.MineBlock()

	// the wallet should no longer list the resulting output as spendable
	unspentResp, err = testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs:", err)
	}
	for _, o := range unspentResp.Outputs {
		if o.UnlockHash == addr {
			t.Fatal("spent output still listed as spendable")
		}
	}
}

// TestUnspentOutputs tests the UnspentOutputs method of the wallet.
func TestUnspentOutputs(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(siatest.TestDir(t.Name())))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// create a dummy address and send coins to it
	addr := types.UnlockHash{1}

	_, err = testNode.WalletSiacoinsPost(types.SiacoinPrecision.Mul64(77), addr)
	if err != nil {
		t.Fatal(err)
	}
	testNode.MineBlock()

	// define a helper function to check whether addr appears in
	// UnspentOutputs
	addrIsPresent := func() bool {
		wug, err := testNode.WalletUnspentGet()
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range wug.Outputs {
			if o.UnlockHash == addr {
				return true
			}
		}
		return false
	}

	// initially, the output should not show up in UnspentOutputs, because the
	// address is not being tracked yet
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs yet")
	}

	// add the address, but tell the wallet it hasn't been used yet. The
	// wallet won't rescan, so it still won't see any outputs.
	err = testNode.WalletWatchAddPost([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs yet")
	}

	// remove the address, then add it again, this time telling the wallet
	// that it has been used.
	err = testNode.WalletWatchRemovePost([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = testNode.WalletWatchAddPost([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}

	// output should now show up
	if !addrIsPresent() {
		t.Fatal("addr not present in UnspentOutputs after AddWatchAddresses")
	}

	// remove the address, but tell the wallet that the address hasn't been
	// used. The wallet won't rescan, so the output should still show up.
	err = testNode.WalletWatchRemovePost([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !addrIsPresent() {
		t.Fatal("addr should still be present in UnspentOutputs")
	}

	// add and remove the address again, this time triggering a rescan. The
	// output should no longer appear.
	err = testNode.WalletWatchAddPost([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = testNode.WalletWatchRemovePost([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs")
	}
}

// TestFileContractUnspentOutputs tests that outputs created from file
// contracts are properly handled by the wallet.
func TestFileContractUnspentOutputs(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	gp := siatest.GroupParams{
		Hosts:   1,
		Renters: 1,
		Miners:  1,
	}
	tg, err := siatest.NewGroupFromTemplate(siatest.TestDir(t.Name()), gp)
	if err != nil {
		t.Fatal(err)
	}
	defer tg.Close()

	// pick a renter contract
	renter := tg.Renters()[0]
	rc, err := renter.RenterContractsGet()
	if err != nil {
		t.Fatal(err)
	}
	contract := rc.ActiveContracts[0]

	// mine until the contract has ended
	miner := tg.Miners()[0]
	for i := types.BlockHeight(0); i < contract.EndHeight; i++ {
		miner.MineBlock()
	}

	// wallet should report the unspent output (the storage proof is missed
	// because we did not upload any data to the contract -- the host has no
	// incentive to submit a proof)
	outputID := contract.ID.StorageProofOutputID(types.ProofMissed, 0)
	wug, err := renter.WalletUnspentGet()
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, o := range wug.Outputs {
		if types.SiacoinOutputID(o.ID) == outputID {
			found = true
		}
	}
	if !found {
		t.Fatal("wallet's spendable outputs did not contain file contract output")
	}
}
