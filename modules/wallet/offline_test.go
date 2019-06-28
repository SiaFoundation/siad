package wallet

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

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

	// load siafunds into the wallet
	err = wt.wallet.LoadSiagKeys(crypto.NewWalletKey(crypto.HashObject(wt.walletMasterKey)), []string{"../../types/siag0of1of1.siakey"})
	if err != nil {
		t.Error(err)
	}

	// get a siacoin output and a siafund output
	outputs, err := wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	var sco, sfo modules.UnspentOutput
	for _, o := range outputs {
		if o.FundType == types.SpecifierSiacoinOutput {
			sco = o
		} else if o.FundType == types.SpecifierSiafundOutput {
			sfo = o
		}
	}
	scuc, err := wt.wallet.UnlockConditions(sco.UnlockHash)
	if err != nil {
		t.Fatal(err)
	}
	sfuc, err := wt.wallet.UnlockConditions(sfo.UnlockHash)
	if err != nil {
		t.Fatal(err)
	}

	// create a transaction that sends both outputs to the void
	txn := types.Transaction{
		SiafundInputs: []types.SiafundInput{{
			ParentID:         types.SiafundOutputID(sfo.ID),
			UnlockConditions: sfuc,
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Value:      sfo.Value,
			UnlockHash: types.UnlockHash{},
		}},

		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         types.SiacoinOutputID(sco.ID),
			UnlockConditions: scuc,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      sco.Value,
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:      crypto.Hash(sco.ID),
				CoveredFields: types.CoveredFields{WholeTransaction: true},
			},
			{
				ParentID:      crypto.Hash(sfo.ID),
				CoveredFields: types.CoveredFields{WholeTransaction: true},
			},
		},
	}

	// sign the transaction
	err = wt.wallet.SignTransaction(&txn, nil)
	if err != nil {
		t.Fatal(err)
	}
	// txn should now have signatures
	if len(txn.TransactionSignatures[0].Signature) == 0 || len(txn.TransactionSignatures[1].Signature) == 0 {
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

	// the wallet should no longer list the outputs as spendable
	outputs, err = wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range outputs {
		if o.ID == sco.ID || o.ID == sfo.ID {
			t.Fatal("spent output still listed as spendable")
		}
	}
}

// TestSignTransactionNoWallet tests the SignTransaction function.
func TestSignTransactionNoWallet(t *testing.T) {
	// generate a seed
	var seed modules.Seed
	fastrand.Read(seed[:])

	// generate a key from that seed. Use a random index < 1000 to test
	// whether SignTransaction can find it.
	sk := generateSpendableKey(seed, fastrand.Uint64n(1000))

	// create a transaction that sends 1 SC and 1 SF to the void
	txn := types.Transaction{
		SiacoinInputs: []types.SiacoinInput{{
			ParentID:         types.SiacoinOutputID{1}, // doesn't need to actually exist
			UnlockConditions: sk.UnlockConditions,
		}},
		SiacoinOutputs: []types.SiacoinOutput{{
			Value:      types.NewCurrency64(1),
			UnlockHash: types.UnlockHash{},
		}},
		SiafundInputs: []types.SiafundInput{{
			ParentID:         types.SiafundOutputID{2}, // doesn't need to actually exist
			UnlockConditions: sk.UnlockConditions,
		}},
		SiafundOutputs: []types.SiafundOutput{{
			Value:      types.NewCurrency64(1),
			UnlockHash: types.UnlockHash{},
		}},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:      crypto.Hash{1},
				CoveredFields: types.CoveredFields{WholeTransaction: true},
			},
			{
				ParentID:      crypto.Hash{2},
				CoveredFields: types.CoveredFields{WholeTransaction: true},
			},
		},
	}

	// can't sign without toSign argument
	if err := SignTransaction(&txn, seed, nil, 0); err == nil {
		t.Fatal("expected error when attempting to sign without specifying ParentIDs")
	}

	// sign the transaction
	toSign := []crypto.Hash{
		txn.TransactionSignatures[0].ParentID,
		txn.TransactionSignatures[1].ParentID,
	}
	if err := SignTransaction(&txn, seed, toSign, 0); err != nil {
		t.Fatal(err)
	}
	// txn should now have a signature
	if len(txn.TransactionSignatures[0].Signature) == 0 {
		t.Fatal("transaction was not signed")
	}
	// the resulting transaction should be valid
	if err := txn.StandaloneValid(0); err != nil {
		t.Fatal(err)
	}
}

// TestUnspentOutputs tests the UnspentOutputs method of the wallet.
func TestUnspentOutputs(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.closeWt()

	// create a dummy address and send coins to it
	addr := types.UnlockHash{1}

	_, err = wt.wallet.SendSiacoins(types.SiacoinPrecision.Mul64(77), addr)
	if err != nil {
		t.Fatal(err)
	}
	wt.miner.AddBlock()

	// define a helper function to check whether addr appears in
	// UnspentOutputs
	addrIsPresent := func() bool {
		outputs, err := wt.wallet.UnspentOutputs()
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range outputs {
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
	err = wt.wallet.AddWatchAddresses([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs yet")
	}

	// remove the address, then add it again, this time telling the wallet
	// that it has been used.
	err = wt.wallet.RemoveWatchAddresses([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.wallet.AddWatchAddresses([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}

	// output should now show up
	if !addrIsPresent() {
		t.Fatal("addr not present in UnspentOutputs after AddWatchAddresses")
	}

	// remove the address, but tell the wallet that the address hasn't been
	// used. The wallet won't rescan, so the output should still show up.
	err = wt.wallet.RemoveWatchAddresses([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !addrIsPresent() {
		t.Fatal("addr should still be present in UnspentOutputs")
	}

	// add and remove the address again, this time triggering a rescan. The
	// output should no longer appear.
	err = wt.wallet.AddWatchAddresses([]types.UnlockHash{addr}, true)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.wallet.RemoveWatchAddresses([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs")
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

	// define a helper function to check whether addr appears in
	// UnspentOutputs
	addrIsPresent := func() bool {
		outputs, err := wt.wallet.UnspentOutputs()
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range outputs {
			if o.UnlockHash == addr {
				return true
			}
		}
		return false
	}

	// the output should not show up in UnspentOutputs, because the address is
	// not being tracked yet
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs")
	}

	// track the address
	err = wt.wallet.AddWatchAddresses([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}

	// the address will now show up in WatchAddresses. Even though we haven't
	// mined the block sending the coins yet, the output will show up in
	// UnspentOutputs because it's in the transaction pool.
	addrs, err := wt.wallet.WatchAddresses()
	if err != nil {
		t.Fatal(err)
	} else if len(addrs) != 1 || addrs[0] != addr {
		t.Fatal("expecting addr to be watched, got", addrs)
	}
	if !addrIsPresent() {
		t.Fatal("addr not present in UnspentOutputs after AddWatchAddresses")
	}

	// mine the block; the output should still be present.
	wt.miner.AddBlock()
	if !addrIsPresent() {
		t.Fatal("addr not present in UnspentOutputs after AddWatchAddresses")
	}

	// create a transaction that sends an output to the void
	outputs, err := wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	}
	var output modules.UnspentOutput
	for _, output = range outputs {
		if output.UnlockHash == addr {
			break
		}
	}
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
	sig := crypto.SignHash(txn.SigHash(0, wt.cs.Height()), sk.SecretKeys[0])
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
	if addrIsPresent() {
		t.Fatal("shouldn't see addr in UnspentOutputs after spending it")
	}
	// stop tracking the address
	err = wt.wallet.RemoveWatchAddresses([]types.UnlockHash{addr}, false)
	if err != nil {
		t.Fatal(err)
	}
	// the address should no longer appear in WatchAddresses
	addrs, err = wt.wallet.WatchAddresses()
	if err != nil {
		t.Fatal(err)
	} else if len(addrs) != 0 {
		t.Fatal("expecting no watched addresses, got", addrs)
	}
}

// TestUnlockConditions tests the UnlockConditions and AddUnlockConditions
// methods of the wallet.
func TestUnlockConditions(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer wt.closeWt()

	// add some random unlock conditions
	sk := generateSpendableKey(modules.Seed{}, 1234)
	if err := wt.wallet.AddUnlockConditions(sk.UnlockConditions); err != nil {
		t.Fatal(err)
	}

	// the unlock conditions should now be listed for the address
	uc, err := wt.wallet.UnlockConditions(sk.UnlockConditions.UnlockHash())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(uc, sk.UnlockConditions) {
		t.Fatal("unlock conditions do not match")
	}

	// no unlock conditions should be returned for a random address
	uc, err = wt.wallet.UnlockConditions(types.UnlockHash{1})
	if err == nil {
		t.Fatal("expected error when requested unlock conditions of random address")
	}
}
