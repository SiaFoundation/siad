package wallet

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestSignTransaction constructs a valid, signed transaction using the
// wallet's UnspentOutputs and SignTransaction methods.
func TestSignTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.closeWt()

	// get an output to spend and its unlock conditions
	outputs, err := wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	uc, err := wt.wallet.UnlockConditions(outputs[0].UnlockHash)
	if err != nil {
		t.Fatal(err)
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
			ParentID:      crypto.Hash(outputs[0].ID),
			CoveredFields: types.CoveredFields{WholeTransaction: true},
		}},
	}

	// sign the transaction
	err = wt.wallet.SignTransaction(&txn, nil)
	if err != nil {
		t.Fatal(err)
	}
	// txn should now have a signature
	if len(txn.TransactionSignatures[0].Signature) == 0 {
		t.Fatal("transaction was not signed")
	}

	// the resulting transaction should be valid; submit it to the tpool and
	// mine a block to confirm it
	height, _ := wt.wallet.Height()
	err = txn.StandaloneValid(height)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.tpool.AcceptTransactionSet([]types.Transaction{txn})
	if err != nil {
		t.Fatal(err)
	}
	err = wt.addBlockNoPayout()
	if err != nil {
		t.Fatal(err)
	}

	// the wallet should no longer list the resulting output as spendable
	outputs, err = wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 {
		t.Fatal("expected one output")
	}
	if outputs[0].ID == types.OutputID(txn.SiacoinInputs[0].ParentID) {
		t.Fatal("spent output still listed as spendable")
	}
}

// TestWatchOnly tests the ability of the wallet to track addresses that it
// does not own.
func TestWatchOnly(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.closeWt()

	// create an address manually and send coins to it
	sk := generateSpendableKey(modules.Seed{}, 1234)
	addr := sk.UnlockConditions.UnlockHash()

	_, err = wt.wallet.SendSiacoins(types.SiacoinPrecision.Mul64(77), addr)
	if err != nil {
		t.Fatal(err)
	}
	wt.miner.AddBlock()

	// the output should not show up in UnspentOutputs, because the address is
	// not being tracked yet
	outputs, err := wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
		if o.UnlockHash == addr {
			t.Fatal("shouldn't see addr in UnspentOutputs yet")
		}
	}

	// track the address
	err = wt.wallet.AddWatchAddresses([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}

	// output should now show up
	var output modules.UnspentOutput
	outputs, err = wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
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
			UnlockConditions: sk.UnlockConditions,
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
	sig := crypto.SignHash(txn.SigHash(0), sk.SecretKeys[0])
	txn.TransactionSignatures[0].Signature = sig[:]

	// the resulting transaction should be valid; submit it to the tpool and
	// mine a block to confirm it
	height, _ := wt.wallet.Height()
	err = txn.StandaloneValid(height)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.tpool.AcceptTransactionSet([]types.Transaction{txn})
	if err != nil {
		t.Fatal(err)
	}
	err = wt.addBlockNoPayout()
	if err != nil {
		t.Fatal(err)
	}

	// the wallet should no longer list the resulting output as spendable
	outputs, err = wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
		if o.UnlockHash == addr {
			t.Fatal("spent output still listed as spendable")
		}
	}
}
