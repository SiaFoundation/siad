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

	testdir, err := siatest.TestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

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

	testdir, err := siatest.TestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(testdir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := testNode.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// get an output to spend
	unspentResp, err := testNode.WalletUnspentGet()
	if err != nil {
		t.Fatal("failed to get spendable outputs:", err)
	}
	outputs := unspentResp.Outputs
	uc, err := testNode.WalletUnlockConditionsGet(outputs[0].UnlockHash)
	if err != nil {
		t.Fatal("failed to get unlock conditions:", err)
	}

	// create a transaction that sends an output to the void
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         types.SiacoinOutputID(outputs[0].ID),
			UnlockConditions: uc,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      outputs[0].Value,
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: []types.TransactionSignature{{
			ParentID: crypto.Hash(outputs[0].ID),
		}},
	}

	// sign the transaction
	signResp, err := testNode.WalletSignPost(txn, nil)
	if err != nil {
		t.Fatal("failed to sign the transaction", err)
	}
	txn = signResp.Transaction

	// txn should now have a signature
	if len(txn.TransactionSignatures) == 0 {
		t.Fatal("transaction was not signed")
	}

	// the resulting transaction should be valid; submit it to the tpool and
	// mine a block to confirm it
	if err := testNode.TransactionpoolRawPost(nil, txn); err != nil {
		t.Fatal("failed to add transaction to pool", err)
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

	testdir, err := siatest.TestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create a new server
	testNode, err := siatest.NewNode(node.AllModules(testdir))
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
	}
	for _, o := range unspentResp.Outputs {
		if o.UnlockHash == addr {
			t.Fatal("shouldn't see addr in UnspentOutputs yet")
		}
	}

	// track the address
	err = testNode.WalletWatchPost([]types.UnlockHash{addr})
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
	sig := crypto.SignHash(txn.SigHash(0), sk)
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
