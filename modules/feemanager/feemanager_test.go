package feemanager

import (
	"encoding/hex"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/consensus"
	"gitlab.com/NebulousLabs/Sia/modules/gateway"
	"gitlab.com/NebulousLabs/Sia/modules/transactionpool"
	"gitlab.com/NebulousLabs/Sia/modules/wallet"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestFeeManager checks to make sure the creating and closing a FeeManager
// performs as expected and that loading the persistence from disk is as
// expected
func TestFeeManager(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create FeeManager
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Set some Fees
	err = setRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}

	// Record the data to be persisted
	persistData := fm.persistData()

	// Check the Settings
	settings, err := fm.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.CurrentPayout.Cmp(fm.currentPayout) != 0 {
		t.Fatalf("Incorrect Settings: CurrentPayout is %v Expected %v", settings.CurrentPayout, fm.currentPayout)
	}
	if settings.MaxPayout.Cmp(fm.maxPayout) != 0 {
		t.Fatalf("Incorrect Settings: MaxPayout is %v Expected %v", settings.MaxPayout, fm.maxPayout)
	}
	if settings.PayoutHeight != fm.payoutHeight {
		t.Fatalf("Incorrect Settings: PayoutHeight is %v Expected %v", settings.PayoutHeight, fm.payoutHeight)
	}

	// Close FeeManager
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Load a new FeeManager from the same persist directory
	fm2, err := New(fm.staticCS, fm.staticWallet, fm.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm2.Close()

	// Verify the persistence was loaded as expected
	err = verifyLoadedPersistence(fm2, persistData)
	if err != nil {
		t.Fatal(err)
	}
}

// TestFeeManagerSetAndCancel makes sure the the SetFee and CancelFee methods
// perform as expected
func TestFeeManagerSetAndCancel(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create FeeManager
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer fm.Close()

	// Set some Fees
	err = setRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}

	// Get the Fees
	fees, paid, err := fm.Fees()
	if err != nil {
		t.Fatal(err)
	}

	// Verify all the fees were set
	numFees := len(fees)
	if numFees != len(fm.fees) {
		t.Fatalf("Not all fees recorded, expected %v fees but found %v", numFees, len(fm.fees))
	}
	if len(paid) != 0 {
		t.Fatalf("Shouldn't have any paid fees but found %v", len(paid))
	}

	// Cancel a random fee
	i := fastrand.Intn(len(fees))
	canceledUID := fees[i].UID
	err = fm.CancelFee(canceledUID)
	if err != nil {
		t.Fatal(err)
	}

	// Get the Fees
	fees, paid, err = fm.Fees()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the number of fees
	if _, ok := fm.fees[canceledUID]; ok {
		t.Fatal("Fee not removed from the map")
	}
	if numFees-1 != len(fm.fees) {
		t.Fatalf("Expected %v fees in the map but found %v", numFees-1, len(fm.fees))
	}
	if numFees-1 != len(fees) {
		t.Fatalf("Expected %v fees but found %v", numFees-1, len(fees))
	}
	if len(paid) != 0 {
		t.Fatalf("Shouldn't have any paid fees but found %v", len(paid))
	}

	// Check the number of Fees in the Fees Persist File
	persistedFees, err := fm.loadAllFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedFees) != numFees {
		t.Fatalf("Expected %v fees to be persisted but found %v", numFees, len(persistedFees))
	}

	// Load a new FeeManager from the same persist directory and verify the fee
	// cancel was persisted
	fm2, err := New(fm.staticCS, fm.staticWallet, fm.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	defer fm2.Close()
	if _, ok := fm2.fees[canceledUID]; ok {
		t.Fatal("Fee not removed from the map")
	}
	if numFees-1 != len(fm2.fees) {
		t.Fatalf("Expected %v fees in the map but found %v", numFees-1, len(fm2.fees))
	}

	// Set a fee that would exceed the maxPayout to ensure that it fails
	err = fm2.SetFee(types.UnlockHash{}, fm2.maxPayout.Add(defaultMaxPayout), modules.AppUID("expensive"), true)
	if err == nil {
		t.Fatal("Setting expensive fee should fail")
	}
}

// newTestingFeeManager creates a FeeManager for testing
func newTestingFeeManager(testName string) (*FeeManager, error) {
	// Create testdir
	testDir := build.TempDir("feemanager", testName)

	// Create Dependencies
	cs, w, err := testingDependencies(testDir)
	if err != nil {
		return nil, err
	}

	// Return FeeManager
	return NewCustomFeeManager(cs, w, filepath.Join(testDir, modules.FeeManagerDir), "", modules.ProdDependencies)
}

// testingDependencies creates the dependencies needed for the FeeManager
func testingDependencies(testdir string) (modules.ConsensusSet, modules.Wallet, error) {
	// Create a gateway
	g, err := gateway.New("localhost:0", false, filepath.Join(testdir, modules.GatewayDir))
	if err != nil {
		return nil, nil, err
	}
	// Create a consensus set
	cs, errChan := consensus.New(g, false, filepath.Join(testdir, modules.ConsensusDir))
	if err := <-errChan; err != nil {
		return nil, nil, err
	}
	// Create a tpool
	tp, err := transactionpool.New(cs, g, filepath.Join(testdir, modules.TransactionPoolDir))
	if err != nil {
		return nil, nil, err
	}
	// Create a wallet and unlock it
	w, err := wallet.New(cs, tp, filepath.Join(testdir, modules.WalletDir))
	if err != nil {
		return nil, nil, err
	}
	key := crypto.GenerateSiaKey(crypto.TypeDefaultWallet)
	_, err = w.Encrypt(key)
	if err != nil {
		return nil, nil, err
	}
	err = w.Unlock(key)
	if err != nil {
		return nil, nil, err
	}

	return cs, w, nil
}

// setRandomFees is a helper function to set a random number of fees for the
// FeeManager. It will always set at least 1
func setRandomFees(fm *FeeManager) error {
	for i := 0; i < fastrand.Intn(5)+1; i++ {
		amount := types.NewCurrency64(fastrand.Uint64n(100))
		appUID := modules.AppUID(hex.EncodeToString(fastrand.Bytes(20)))
		recurring := fastrand.Intn(100)%2 == 0
		err := fm.SetFee(types.UnlockHash{}, amount, appUID, recurring)
		if err != nil {
			return err
		}
	}
	return nil
}
