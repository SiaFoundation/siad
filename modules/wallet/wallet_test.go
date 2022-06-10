package wallet

import (
	"path/filepath"
	"sort"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/consensus"
	"go.sia.tech/siad/modules/gateway"
	"go.sia.tech/siad/modules/miner"
	"go.sia.tech/siad/modules/transactionpool"
	"go.sia.tech/siad/types"
)

// A Wallet tester contains a ConsensusTester and has a bunch of helpful
// functions for facilitating wallet integration testing.
type walletTester struct {
	cs      modules.ConsensusSet
	gateway modules.Gateway
	tpool   modules.TransactionPool
	miner   modules.TestMiner
	wallet  *Wallet

	walletMasterKey crypto.CipherKey

	persistDir string
}

// createWalletTester takes a testing.T and creates a WalletTester.
func createWalletTester(name string, deps modules.Dependencies) (*walletTester, error) {
	// Create the modules
	testdir := build.TempDir(modules.WalletDir, name)
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, err
	}
	w, err := NewCustomWallet(cs, tp, filepath.Join(testdir, modules.WalletDir), deps)
	if err != nil {
		return nil, err
	}
	masterKey := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	_, err = w.Encrypt(masterKey)
	if err != nil {
		return nil, err
	}
	err = w.Unlock(masterKey)
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}

	// Assemble all components into a wallet tester.
	wt := &walletTester{
		cs:      cs,
		gateway: g,
		tpool:   tp,
		miner:   m,
		wallet:  w,

		walletMasterKey: masterKey,

		persistDir: testdir,
	}

	// Mine blocks until there is money in the wallet.
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		b, _ := wt.miner.FindBlock()
		err := wt.cs.AcceptBlock(b)
		if err != nil {
			return nil, err
		}
	}
	return wt, nil
}

// createBlankWalletTester creates a wallet tester that has not mined any
// blocks or encrypted the wallet.
func createBlankWalletTester(name string) (*walletTester, error) {
	// Create the modules
	testdir := build.TempDir(modules.WalletDir, name)
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, err
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, err
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, err
	}
	w, err := New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, err
	}
	m, err := miner.New(cs, tp, w, filepath.Join(testdir, modules.MinerDir))
	if err != nil {
		return nil, err
	}

	// Assemble all components into a wallet tester.
	wt := &walletTester{
		gateway: g,
		cs:      cs,
		tpool:   tp,
		miner:   m,
		wallet:  w,

		persistDir: testdir,
	}
	return wt, nil
}

// closeWt closes all of the modules in the wallet tester.
func (wt *walletTester) closeWt() error {
	errs := []error{
		wt.gateway.Close(),
		wt.cs.Close(),
		wt.tpool.Close(),
		wt.miner.Close(),
		wt.wallet.Close(),
	}
	return build.JoinErrors(errs, "; ")
}

// TestNilInputs tries starting the wallet using nil inputs.
func TestNilInputs(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	testdir := build.TempDir(modules.WalletDir, t.Name())
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		t.Fatal(err)
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		t.Fatal(err)
	}

	wdir := filepath.Join(testdir, modules.WalletDir)
	_, err = New(cs, nil, wdir)
	if !errors.Contains(err, errNilTpool) {
		t.Error(err)
	}
	_, err = New(nil, tp, wdir)
	if !errors.Contains(err, errNilConsensusSet) {
		t.Error(err)
	}
	_, err = New(nil, nil, wdir)
	if !errors.Contains(err, errNilConsensusSet) {
		t.Error(err)
	}
}

// TestAllAddresses checks that AllAddresses returns all of the wallet's
// addresses in sorted order.
func TestAllAddresses(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	wt, err := createBlankWalletTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	wt.wallet.keys[types.UnlockHash{1}] = spendableKey{}
	wt.wallet.keys[types.UnlockHash{5}] = spendableKey{}
	wt.wallet.keys[types.UnlockHash{0}] = spendableKey{}
	wt.wallet.keys[types.UnlockHash{2}] = spendableKey{}
	wt.wallet.keys[types.UnlockHash{4}] = spendableKey{}
	wt.wallet.keys[types.UnlockHash{3}] = spendableKey{}
	addrs, err := wt.wallet.AllAddresses()
	if err != nil {
		t.Fatal(err)
	}
	for i := range addrs {
		if addrs[i][0] != byte(i) {
			t.Error("address sorting failed:", i, addrs[i][0])
		}
	}
}

// TestCloseWallet tries to close the wallet.
func TestCloseWallet(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	testdir := build.TempDir(modules.WalletDir, t.Name())
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		t.Fatal(err)
	}
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		t.Fatal(err)
	}
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		t.Fatal(err)
	}
	wdir := filepath.Join(testdir, modules.WalletDir)
	w, err := New(cs, tp, wdir)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestRescanning verifies that calling Rescanning during a scan operation
// returns true, and false otherwise.
func TestRescanning(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	// A fresh wallet should not be rescanning.
	rescanning, err := wt.wallet.Rescanning()
	if err != nil {
		t.Fatal(err)
	}
	if rescanning {
		t.Fatal("fresh wallet should not report that a scan is underway")
	}

	// lock the wallet
	err = wt.wallet.Lock()
	if err != nil {
		t.Fatal(err)
	}

	// spawn an unlock goroutine
	errChan := make(chan error)
	go func() {
		// acquire the write lock so that Unlock acquires the trymutex, but
		// cannot proceed further
		wt.wallet.mu.Lock()
		errChan <- wt.wallet.Unlock(wt.walletMasterKey)
	}()

	// wait for goroutine to start, after which Rescanning should return true
	time.Sleep(time.Millisecond * 10)
	rescanning, err = wt.wallet.Rescanning()
	if err != nil {
		t.Fatal(err)
	}
	if !rescanning {
		t.Fatal("wallet should report that a scan is underway")
	}

	// release the mutex and allow the call to complete
	wt.wallet.mu.Unlock()
	if err := <-errChan; err != nil {
		t.Fatal("unlock failed:", err)
	}

	// Rescanning should now return false again
	rescanning, err = wt.wallet.Rescanning()
	if err != nil {
		t.Fatal(err)
	}
	if rescanning {
		t.Fatal("wallet should not report that a scan is underway")
	}
}

// TestFutureAddressGeneration checks if the right amount of future addresses
// is generated after calling NextAddress() or locking + unlocking the wallet.
func TestLookaheadGeneration(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	// Check if number of future keys is correct
	wt.wallet.mu.RLock()
	progress, err := dbGetPrimarySeedProgress(wt.wallet.dbTx)
	wt.wallet.mu.RUnlock()
	if err != nil {
		t.Fatal("Couldn't fetch primary seed from db")
	}

	actualKeys := uint64(len(wt.wallet.lookahead))
	expectedKeys := maxLookahead(progress)
	if actualKeys != expectedKeys {
		t.Errorf("expected len(lookahead) == %d but was %d", actualKeys, expectedKeys)
	}

	// Generate some more keys
	for i := 0; i < 100; i++ {
		wt.wallet.NextAddress()
	}

	// Lock and unlock
	err = wt.wallet.Lock()
	if err != nil {
		t.Fatal(err)
	}
	err = wt.wallet.Unlock(wt.walletMasterKey)
	if err != nil {
		t.Fatal(err)
	}

	wt.wallet.mu.RLock()
	progress, err = dbGetPrimarySeedProgress(wt.wallet.dbTx)
	wt.wallet.mu.RUnlock()
	if err != nil {
		t.Fatal("Couldn't fetch primary seed from db")
	}

	actualKeys = uint64(len(wt.wallet.lookahead))
	expectedKeys = maxLookahead(progress)
	if actualKeys != expectedKeys {
		t.Errorf("expected len(lookahead) == %d but was %d", actualKeys, expectedKeys)
	}

	wt.wallet.mu.RLock()
	defer wt.wallet.mu.RUnlock()
	for i := range wt.wallet.keys {
		_, exists := wt.wallet.lookahead[i]
		if exists {
			t.Fatal("wallet keys contained a key which is also present in lookahead")
		}
	}
}

// TestAdvanceLookaheadNoRescan tests if a transaction to multiple lookahead addresses
// is handled correctly without forcing a wallet rescan.
func TestAdvanceLookaheadNoRescan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	builder, err := wt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	payout := types.ZeroCurrency

	// Get the current progress
	wt.wallet.mu.RLock()
	progress, err := dbGetPrimarySeedProgress(wt.wallet.dbTx)
	wt.wallet.mu.RUnlock()
	if err != nil {
		t.Fatal("Couldn't fetch primary seed from db")
	}

	// choose 10 keys in the lookahead and remember them
	var receivingAddresses []types.UnlockHash
	for _, sk := range generateKeys(wt.wallet.primarySeed, progress, 10) {
		sco := types.SiacoinOutput{
			UnlockHash: sk.UnlockConditions.UnlockHash(),
			Value:      types.NewCurrency64(1e3),
		}

		builder.AddSiacoinOutput(sco)
		payout = payout.Add(sco.Value)
		receivingAddresses = append(receivingAddresses, sk.UnlockConditions.UnlockHash())
	}

	err = builder.FundSiacoins(payout)
	if err != nil {
		t.Fatal(err)
	}

	tSet, err := builder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}

	err = wt.tpool.AcceptTransactionSet(tSet)
	if err != nil {
		t.Fatal(err)
	}

	_, err = wt.miner.AddBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Check if the receiving addresses were moved from future keys to keys
	wt.wallet.mu.RLock()
	defer wt.wallet.mu.RUnlock()
	for _, uh := range receivingAddresses {
		_, exists := wt.wallet.lookahead[uh]
		if exists {
			t.Fatal("UnlockHash still exists in wallet lookahead")
		}

		_, exists = wt.wallet.keys[uh]
		if !exists {
			t.Fatal("UnlockHash not in map of spendable keys")
		}
	}
}

// TestAdvanceLookaheadNoRescan tests if a transaction to multiple lookahead addresses
// is handled correctly forcing a wallet rescan.
func TestAdvanceLookaheadForceRescan(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	// Mine blocks without payouts so that the balance stabilizes
	for i := types.BlockHeight(0); i < types.MaturityDelay; i++ {
		if err := wt.addBlockNoPayout(); err != nil {
			t.Fatal(err)
		}
	}

	// Get the current progress and balance
	wt.wallet.mu.RLock()
	progress, err := dbGetPrimarySeedProgress(wt.wallet.dbTx)
	wt.wallet.mu.RUnlock()
	if err != nil {
		t.Fatal("Couldn't fetch primary seed from db")
	}
	startBal, _, _, err := wt.wallet.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}

	// Send coins to an address with a high seed index, just outside the
	// lookahead range. It will not be initially detected, but later the
	// rescan should find it.
	highIndex := progress + uint64(len(wt.wallet.lookahead)) + 5
	farAddr := generateSpendableKey(wt.wallet.primarySeed, highIndex).UnlockConditions.UnlockHash()
	farPayout := types.SiacoinPrecision.Mul64(8888)

	builder, err := wt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	builder.AddSiacoinOutput(types.SiacoinOutput{
		UnlockHash: farAddr,
		Value:      farPayout,
	})
	err = builder.FundSiacoins(farPayout)
	if err != nil {
		t.Fatal(err)
	}

	txnSet, err := builder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}

	err = wt.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.addBlockNoPayout(); err != nil {
		t.Fatal(err)
	}
	newBal, _, _, err := wt.wallet.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if !startBal.Sub(newBal).Equals(farPayout) {
		t.Fatal("wallet should not recognize coins sent to very high seed index")
	}

	builder, err = wt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	var payout types.Currency

	// choose 10 keys in the lookahead and remember them
	var receivingAddresses []types.UnlockHash
	for uh, index := range wt.wallet.lookahead {
		// Only choose keys that force a rescan
		if index < progress+lookaheadRescanThreshold {
			continue
		}
		sco := types.SiacoinOutput{
			UnlockHash: uh,
			Value:      types.SiacoinPrecision.Mul64(1000),
		}
		builder.AddSiacoinOutput(sco)
		payout = payout.Add(sco.Value)
		receivingAddresses = append(receivingAddresses, uh)

		if len(receivingAddresses) >= 10 {
			break
		}
	}

	err = builder.FundSiacoins(payout)
	if err != nil {
		t.Fatal(err)
	}

	txnSet, err = builder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}

	err = wt.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.addBlockNoPayout(); err != nil {
		t.Fatal(err)
	}

	// Allow the wallet rescan to finish
	time.Sleep(time.Second * 2)

	// Check that high seed index txn was discovered in the rescan
	rescanBal, _, _, err := wt.wallet.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	if !rescanBal.Equals(startBal) {
		t.Fatal("wallet did not discover txn after rescan")
	}

	// Check if the receiving addresses were moved from future keys to keys
	wt.wallet.mu.RLock()
	defer wt.wallet.mu.RUnlock()
	for _, uh := range receivingAddresses {
		_, exists := wt.wallet.lookahead[uh]
		if exists {
			t.Fatal("UnlockHash still exists in wallet lookahead")
		}

		_, exists = wt.wallet.keys[uh]
		if !exists {
			t.Fatal("UnlockHash not in map of spendable keys")
		}
	}
}

// TestDistantWallets tests if two wallets that use the same seed stay
// synchronized.
func TestDistantWallets(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.closeWt(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create another wallet with the same seed.
	w2, err := New(wt.cs, wt.tpool, build.TempDir(modules.WalletDir, t.Name()+"2", modules.WalletDir))
	if err != nil {
		t.Fatal(err)
	}
	err = w2.InitFromSeed(nil, wt.wallet.primarySeed)
	if err != nil {
		t.Fatal(err)
	}
	sk := crypto.NewWalletKey(crypto.HashObject(wt.wallet.primarySeed))
	err = w2.Unlock(sk)
	if err != nil {
		t.Fatal(err)
	}

	// Use the first wallet.
	for i := uint64(0); i < lookaheadBuffer/2; i++ {
		_, err = wt.wallet.SendSiacoins(types.SiacoinPrecision, types.UnlockHash{})
		if err != nil {
			t.Fatal(err)
		}
		if err := wt.addBlockNoPayout(); err != nil {
			t.Fatal(err)
		}
	}

	// The second wallet's balance should update accordingly.
	w1bal, _, _, err := wt.wallet.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}
	w2bal, _, _, err := w2.ConfirmedBalance()
	if err != nil {
		t.Fatal(err)
	}

	if !w1bal.Equals(w2bal) {
		t.Fatal("balances do not match:", w1bal, w2bal)
	}

	// Send coins to an address with a very high seed index, outside the
	// lookahead range. w2 should not detect it.
	tbuilder, err := wt.wallet.StartTransaction()
	if err != nil {
		t.Fatal(err)
	}
	farAddr := generateSpendableKey(wt.wallet.primarySeed, lookaheadBuffer*10).UnlockConditions.UnlockHash()
	value := types.SiacoinPrecision.Mul64(1e3)
	tbuilder.AddSiacoinOutput(types.SiacoinOutput{
		UnlockHash: farAddr,
		Value:      value,
	})
	err = tbuilder.FundSiacoins(value)
	if err != nil {
		t.Fatal(err)
	}
	txnSet, err := tbuilder.Sign(true)
	if err != nil {
		t.Fatal(err)
	}
	err = wt.tpool.AcceptTransactionSet(txnSet)
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.addBlockNoPayout(); err != nil {
		t.Fatal(err)
	}

	if newBal, _, _, err := w2.ConfirmedBalance(); !newBal.Equals(w2bal.Sub(value)) {
		if err != nil {
			t.Fatal(err)
		}
		t.Fatal("wallet should not recognize coins sent to very high seed index")
	}
}

func TestWalletFundTransaction(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	wt, err := createWalletTester(t.Name(), modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		wt.closeWt()
	})

	// mine more blocks so the wallet has more than 1 UTXO
	for i := types.BlockHeight(0); i <= types.MaturityDelay; i++ {
		b, _ := wt.miner.FindBlock()
		err := wt.cs.AcceptBlock(b)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Get the wallet's unspent outputs.
	unspentOutputs, err := wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	} else if len(unspentOutputs) == 0 {
		t.Fatal("expected to have unspent outputs")
	}

	// sort the outputs by value, descending
	sort.Slice(unspentOutputs, func(i, j int) bool {
		return unspentOutputs[i].Value.Cmp(unspentOutputs[j].Value) > 0
	})

	// build a transaction that fully spends three UTXOs
	var fundAmount types.Currency
	for _, o := range unspentOutputs[:3] {
		if o.FundType != types.SpecifierSiacoinOutput {
			t.Fatal("expected all outputs to be siacoin outputs")
		}

		fundAmount = fundAmount.Add(o.Value)
	}

	txn := types.Transaction{
		SiacoinOutputs: []types.SiacoinOutput{{Value: fundAmount, UnlockHash: types.UnlockHash{}}},
	}

	toSign, cleanup, err := wt.wallet.FundTransaction(&txn, fundAmount)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// verify that the transaction has the correct number of inputs, outputs,
	// and signatures.
	switch {
	case len(txn.SiacoinInputs) != 3:
		t.Fatal("transaction should have three siacoin inputs")
	case len(txn.SiacoinOutputs) != 1:
		t.Fatal("transaction should have one siacoin output")
	case len(txn.TransactionSignatures) != 3:
		t.Fatal("transaction should have three signatures")
	case len(toSign) != 3:
		t.Fatalf("expected 3 sigs, got %v", len(toSign))
	case !txn.SiacoinOutputs[0].Value.Equals(fundAmount):
		t.Fatalf("transaction output 0 has incorrect value: expected %v got %v", fundAmount, txn.SiacoinOutputs[0].Value)
	}

	// sign, broadcast, and mine the transaction
	if err := wt.wallet.SignTransaction(&txn, toSign); err != nil {
		t.Fatal(err)
	} else if err = wt.tpool.AcceptTransactionSet([]types.Transaction{txn}); err != nil {
		t.Fatal(err)
	} else if err := wt.addBlockNoPayout(); err != nil {
		t.Fatal(err)
	}

	cleanup()

	// get the wallet's unspent outputs.
	unspentOutputs, err = wt.wallet.UnspentOutputs()
	if err != nil {
		t.Fatal(err)
	} else if len(unspentOutputs) == 0 {
		t.Fatal("expected to have unspent outputs")
	}

	// sort the outputs by value, descending
	sort.Slice(unspentOutputs, func(i, j int) bool {
		return unspentOutputs[i].Value.Cmp(unspentOutputs[j].Value) > 0
	})

	// build a transaction that partially spends a UTXO
	var totalSpent types.Currency
	for _, o := range unspentOutputs[:2] {
		if o.FundType != types.SpecifierSiacoinOutput {
			t.Fatal("expected all outputs to be siacoin outputs")
		}
		totalSpent = totalSpent.Add(o.Value)
	}
	fundAmount = totalSpent.Mul64(2).Div64(3)
	changeAmount := totalSpent.Sub(fundAmount)
	txn = types.Transaction{
		SiacoinOutputs: []types.SiacoinOutput{{Value: fundAmount, UnlockHash: types.UnlockHash{}}},
	}

	toSign, cleanup, err = wt.wallet.FundTransaction(&txn, fundAmount)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// verify that the transaction has the correct number of inputs, outputs,
	// and signatures.
	switch {
	case len(txn.SiacoinInputs) != 2:
		t.Fatal("transaction should have 3 siacoin inputs")
	case len(txn.SiacoinOutputs) != 2:
		t.Fatal("transaction should have 2 siacoin outputs")
	case len(txn.TransactionSignatures) != 2:
		t.Fatal("transaction should have 2 signatures")
	case len(toSign) != 2:
		t.Fatalf("expected 2 sigs, got %v", len(toSign))
	case !txn.SiacoinOutputs[0].Value.Equals(fundAmount):
		t.Fatalf("transaction output 0 has incorrect value: expected %v got %v", fundAmount, txn.SiacoinOutputs[0].Value)
	case !txn.SiacoinOutputs[1].Value.Equals(changeAmount):
		t.Fatalf("transaction change output has incorrect value: expected %v got %v", changeAmount, txn.SiacoinOutputs[1].Value)
	}

	// sign, broadcast, and mine the transaction
	if err := wt.wallet.SignTransaction(&txn, toSign); err != nil {
		t.Fatal(err)
	} else if err = wt.tpool.AcceptTransactionSet([]types.Transaction{txn}); err != nil {
		t.Fatal(err)
	} else if err := wt.addBlockNoPayout(); err != nil {
		t.Fatal(err)
	}
}
