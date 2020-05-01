package feemanager

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/feemanager"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestFeeManager probes the FeeManager
func TestFeeManager(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new FeeManager
	testDir := feeManagerTestDir(t.Name())
	fm, err := siatest.NewCleanNode(node.FeeManager(testDir))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := fm.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Check for initial values
	fmg, err := fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	nexPayoutHeight := fmg.PayoutHeight
	if nexPayoutHeight == 0 {
		t.Fatalf("PayoutHeight is still 0")
	}

	// Define helper checkFees function
	var numPaidFees, numPendingFees int
	checkFees := func() (api.FeeManagerPaidFeesGET, api.FeeManagerPendingFeesGET) {
		fmPaidGet, err := fm.FeeManagerPaidFeesGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(fmPaidGet.PaidFees) != numPaidFees {
			t.Errorf("Expected %v PaidFees but got %v", numPaidFees, len(fmPaidGet.PaidFees))
		}
		fmPendingGet, err := fm.FeeManagerPendingFeesGet()
		if err != nil {
			t.Fatal(err)
		}
		if len(fmPendingGet.PendingFees) != numPendingFees {
			t.Errorf("Expected %v PendingFees but got %v", numPendingFees, len(fmPendingGet.PendingFees))
		}
		return fmPaidGet, fmPendingGet
	}

	// Check initial fees
	_, _ = checkFees()

	// Set a Fee
	amount := types.NewCurrency64(1000)
	address := types.UnlockHash{}
	appUID := modules.AppUID("testapp")
	recurring := fastrand.Intn(2) == 0
	fmap, err := fm.FeeManagerAddPost(address, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fees
	numPendingFees = 1
	_, fmpfg := checkFees()

	fee := fmpfg.PendingFees[0]
	if fee.Address != address {
		t.Errorf("Expected address to be %v but was %v", address, fee.Address)
	}
	if fee.Amount.Cmp(amount) != 0 {
		t.Errorf("Expected amount to be %v but was %v", amount, fee.Amount)
	}
	if fee.AppUID != appUID {
		t.Errorf("Expected AppUID to be %v but was %v", appUID, fee.AppUID)
	}
	if fee.PaymentCompleted {
		t.Error("PaymentCompleted should be false")
	}
	payoutHeight := nexPayoutHeight + feemanager.PayoutInterval
	if fee.PayoutHeight != payoutHeight {
		t.Errorf("Expected PayoutHeight to be %v but was %v", payoutHeight, fee.PayoutHeight)
	}
	if fee.Recurring != recurring {
		t.Errorf("Expected Recurring to be %v but was %v", recurring, fee.Recurring)
	}
	if fee.Timestamp == 0 {
		t.Error("Timestamp is not set")
	}
	if fee.TransactionCreated {
		t.Error("TransactionCreated should be false")
	}
	if fee.FeeUID != fmap.FeeUID {
		t.Errorf("Expected UID to be %v but was %v", fmap.FeeUID, fee.FeeUID)
	}

	// Cancel Fee
	err = fm.FeeManagerCancelPost(fee.FeeUID)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fees
	numPendingFees = 0
	_, _ = checkFees()
}

// TestFeeManagerProcessFee probes the processing of a fee
func TestFeeManagerProcessFee(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a TestGroup with a FeeManager and two Renters. One renter will
	// represent the Developer and one will represent Nebulous
	testDir := feeManagerTestDir(t.Name())
	feeManagerParams := node.FeeManager(filepath.Join(testDir, "feemanager"))
	fmDeps := &dependencies.DependencyCustomNebulousAddress{}
	feeManagerParams.FeeManagerDeps = fmDeps
	minerParams := node.Miner(filepath.Join(testDir, "miner"))
	renterParams := node.Renter(filepath.Join(testDir, "renter"))
	renterParams2 := node.Renter(filepath.Join(testDir, "nebulous"))
	tg, err := siatest.NewGroup(testDir, feeManagerParams, minerParams, renterParams, renterParams2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Grab the nodes
	m := tg.Miners()[0]
	renters := tg.Renters()
	r := renters[0]
	nebulous := renters[1]
	var fm *siatest.TestNode
	for _, node := range tg.Nodes() {
		switch node.Address {
		case m.Address:
		case r.Address:
		case nebulous.Address:
		default:
			fm = node
			break
		}
	}

	// Get a address for the Nebulous wallet and set it for the FeeManager
	// Dependency
	nwag, err := nebulous.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	fmDeps.SetAddress(nwag.Address)

	// Get the starting wallet balances
	nwg, err := nebulous.WalletGet()
	if err != nil {
		t.Fatal(err)
	}
	nebulousStartingBal := nwg.ConfirmedSiacoinBalance
	rwg, err := r.WalletGet()
	if err != nil {
		t.Fatal(err)
	}
	renterStartingBal := rwg.ConfirmedSiacoinBalance
	fmwg, err := fm.WalletGet()
	if err != nil {
		t.Fatal(err)
	}
	fmStartingBal := fmwg.ConfirmedSiacoinBalance

	// Set a fee on the FeeManager with the Renter as the developer
	rwag, err := r.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	amount := types.SiacoinPrecision.Mul64(10)
	appUID := modules.AppUID("testapp")
	recurring := fastrand.Intn(2) == 0
	_, err = fm.FeeManagerAddPost(rwag.Address, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the fee is now a pending fee
	fmpending, err := fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmpending.PendingFees) != 1 {
		t.Fatal("Expected 1 pending fee got", len(fmpending.PendingFees))
	}
	fee := fmpending.PendingFees[0]

	// Get the FeeManager
	fmg, err := fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	nextPayoutHeight := fmg.PayoutHeight
	if nextPayoutHeight == 0 {
		t.Fatalf("PayoutHeight is still 0")
	}

	// Mine blocks to process the fee
	bh, err := fm.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	blocksToMine := int(nextPayoutHeight - bh)
	for i := 0; i <= blocksToMine; i++ {
		err = m.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Check for the wallet balances to reflect the fee being paid out
	tries := 0
	devExpected := renterStartingBal.Add(amount.Mul64(7).Div64(10))
	nebExpected := nebulousStartingBal.Add(amount.Mul64(3).Div64(10))
	fmExpected := fmStartingBal.Sub(amount)
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Mine blocks to make sure transacitons are being confirmed
		if tries%10 == 0 {
			err = m.MineBlock()
			if err != nil {
				return err
			}
		}
		tries++

		// Check Nebulous wallet balanace
		nwg, err = nebulous.WalletGet()
		if err != nil {
			return err
		}
		if nwg.ConfirmedSiacoinBalance.Cmp(nebExpected) != 0 {
			return fmt.Errorf("Incorrect Nebulous wallet balance; expected %v, got %v", nebExpected.HumanString(), nwg.ConfirmedSiacoinBalance.HumanString())
		}

		// Check Renter (Developer) wallet balance
		rwg, err = r.WalletGet()
		if err != nil {
			return err
		}
		if rwg.ConfirmedSiacoinBalance.Cmp(devExpected) != 0 {
			return fmt.Errorf("Incorrect Renter wallet balance; expected %v, got %v", devExpected.HumanString(), rwg.ConfirmedSiacoinBalance.HumanString())
		}

		// Check FeeManager wallet balance
		fmwg, err = fm.WalletGet()
		if err != nil {
			return err
		}
		if fmwg.ConfirmedSiacoinBalance.Cmp(fmExpected) >= 0 {
			return fmt.Errorf("Incorrect FeeManager wallet balance; expected less than %v, got %v", fmExpected.HumanString(), fmwg.ConfirmedSiacoinBalance.HumanString())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Verify the Fees now show as paid
	err = build.Retry(100, 100*time.Millisecond, func() error {
		fmpending, err = fm.FeeManagerPendingFeesGet()
		if err != nil {
			return err
		}
		if len(fmpending.PendingFees) != 0 {
			return fmt.Errorf("FeeManager Still has pending fees %v", fmpending)
		}
		fmpaid, err := fm.FeeManagerPaidFeesGet()
		if err != nil {
			return err
		}
		if len(fmpaid.PaidFees) != 1 {
			return fmt.Errorf("FeeManager should have one paid fee but has %v", len(fmpaid.PaidFees))
		}
		if fmpaid.PaidFees[0].FeeUID != fee.FeeUID {
			return fmt.Errorf("Expected FeeUID of %v but got %v", fee.FeeUID, fmpaid.PaidFees[0].FeeUID)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
