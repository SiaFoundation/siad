package feemanager

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/feemanager"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestFeeManager probes the FeeManager
func TestFeeManager(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a new FeeManager
	testDir := feeManagerTestDir(t.Name())
	fm, err := siatest.NewCleanNode(node.FeeManager(testDir, t.Name()))
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
	if fmg.Settings.PayoutHeight == 0 {
		t.Fatal("bad size")
	}
	fmPaidGet, err := fm.FeeManagerPaidFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPaidGet.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmPaidGet.PaidFees))
	}
	fmPendingGet, err := fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPendingGet.PendingFees) != 0 {
		t.Errorf("Expected 0 PendingFees but got %v", len(fmPendingGet.PendingFees))
	}

	// Set a Fee
	amount := types.NewCurrency64(1000)
	address := types.UnlockHash{}
	appUID := modules.AppUID("testapp")
	recurring := false
	err = fm.FeeManagerAddPost(address, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fee
	fmg, err = fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	fmPaidGet, err = fm.FeeManagerPaidFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPaidGet.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmPaidGet.PaidFees))
	}
	fmPendingGet, err = fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPendingGet.PendingFees) != 1 {
		t.Errorf("Expected 1 PendingFees but got %v", len(fmPendingGet.PendingFees))
	}

	fee := fmPendingGet.PendingFees[0]
	expectedFee := modules.AppFee{
		Address:   address,
		Amount:    amount,
		AppUID:    appUID,
		Recurring: recurring,
		UID:       fee.UID,
	}
	if !reflect.DeepEqual(fee, expectedFee) {
		t.Log("Fee:", fee)
		t.Log("ExpectedFee:", expectedFee)
		t.Error("Fee not as expected")
	}

	// Cancel Fee
	err = fm.FeeManagerCancelPost(fee.UID)
	if err != nil {
		t.Fatal(err)
	}

	// Verify Fee is gone
	fmg, err = fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	fmPaidGet, err = fm.FeeManagerPaidFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPaidGet.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmPaidGet.PaidFees))
	}
	fmPendingGet, err = fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPendingGet.PendingFees) != 0 {
		t.Errorf("Expected 0 PendingFees but got %v", len(fmPendingGet.PendingFees))
	}
}

// TestFeeManagerFeeProcessing probes the processing of the fees by the
// FeeManager
func TestFeeManagerFeeProcessing(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a TestGroup with a Renter and a Miner. The Renter will be the
	// developer that created a fee
	groupParams := siatest.GroupParams{
		Renters: 1,
		Miners:  1,
	}
	groupDir := feeManagerTestDir(t.Name())

	// Specify subtests to run
	subTests := []siatest.SubTest{
		{Name: "TestFeesFailTogether", Test: testFeesFailTogether},
		// These two tests should be written to probe the cases were one type of
		// fee is failing and not the other
		// {Name: "TestOneTimeFeeFail", Test: testOneTimeFeeFail},
		// {Name: "TestRecurringFeeFail", Test: testRecurringFeeFail},
	}

	// Run tests
	if err := siatest.RunSubTests(t, groupParams, groupDir, subTests); err != nil {
		t.Fatal(err)
	}
}

// testFeesFailTogether test the Fee Processing with the fees failing and
// succeeding together
func testFeesFailTogether(t *testing.T, tg *siatest.TestGroup) {
	r := tg.Renters()[0]
	wag, err := r.WalletAddressGet()
	if err != nil {
		t.Fatal(err)
	}
	devAddress := wag.Address
	wg, err := r.WalletGet()
	if err != nil {
		t.Fatal(err)
	}
	devStartingBalance := wg.ConfirmedSiacoinBalance

	// Create a new FeeManager with dependency
	testDir := feeManagerTestDir(t.Name())
	nodeParams := node.FeeManager(testDir, "feemanager")
	dep := dependencies.NewDependencyProcessFeeFail()
	nodeParams.FeeManagerDeps = dep
	nodes, err := tg.AddNodes(nodeParams)
	if err != nil {
		t.Fatal(err)
	}
	fm := nodes[0]
	wg, err = fm.WalletGet()
	if err != nil {
		t.Fatal(err)
	}
	fmStartingBalance := wg.ConfirmedSiacoinBalance

	// Set two Fees with the developer wallet's address. one recurring and one
	// not
	amount := types.SiacoinPrecision.Mul64(500)
	appUID := modules.AppUID("testapp")
	recurring := false
	err = fm.FeeManagerAddPost(devAddress, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}
	totalAmount := amount
	amount = types.SiacoinPrecision.Mul64(300)
	appUID = modules.AppUID("testapp2")
	recurring = true
	err = fm.FeeManagerAddPost(devAddress, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}
	totalAmount = totalAmount.Add(amount)

	// Check for Fees and FeeManager settings
	fmg, err := fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	fmPaidGet, err := fm.FeeManagerPaidFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPaidGet.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmPaidGet.PaidFees))
	}
	fmPendingGet, err := fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPendingGet.PendingFees) != 2 {
		t.Errorf("Expected %v PendingFees but got %v", 2, len(fmPendingGet.PendingFees))
	}

	/*
		// Grab the Recurring fee for reference
		var recurringFee modules.AppFee
		for _, fee := range fmPendingGet.PendingFees {
			if fee.Recurring {
				recurringFee = fee
				break
			}
		}
	*/

	// Mine blocks to trigger the first payout period
	m := tg.Miners()[0]
	bh, err := fm.BlockHeight()
	if err != nil {
		t.Fatal(err)
	}
	payoutHeight := fmg.Settings.PayoutHeight
	blocksToMine := int(payoutHeight-bh) + 1
	for i := 0; i < blocksToMine; i++ {
		err = m.MineBlock()
		if err != nil {
			t.Fatal(err)
		}
	}

	// Process fees should have been triggered twice but neither fee should have
	// been processed. PayoutHeight should be the same and there should be no
	// change in the fees
	fmg, err = fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	if fmg.Settings.PayoutHeight != payoutHeight {
		t.Errorf("PayoutHeight should be %v but was %v", payoutHeight, fmg.Settings.PayoutHeight)
	}
	fmPaidGet, err = fm.FeeManagerPaidFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPaidGet.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmPaidGet.PaidFees))
	}
	fmPendingGet, err = fm.FeeManagerPendingFeesGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmPendingGet.PendingFees) != 2 {
		t.Errorf("Expected %v PendingFees but got %v", 2, len(fmPendingGet.PendingFees))
	}

	// Disable dependency
	dep.Disable()

	// Mine another block
	err = m.MineBlock()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that both fees processed
	fmg, err = fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	// PayoutHeight should have been incremented
	err = build.Retry(100, 100*time.Millisecond, func() error {
		newPayoutHeight := payoutHeight + feemanager.PayoutInterval
		if fmg.Settings.PayoutHeight != newPayoutHeight {
			return fmt.Errorf("PayoutHeight should be %v but was %v", newPayoutHeight, fmg.Settings.PayoutHeight)
		}
		fmPendingGet, err = fm.FeeManagerPendingFeesGet()
		if err != nil {
			return err
		}
		if len(fmPendingGet.PendingFees) != 1 {
			return fmt.Errorf("Expected %v PendingFees but got %v", 1, len(fmPendingGet.PendingFees))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Enable the dep again to avoid additional spending
	dep.Enable()

	// Check for Wallets, Fees, and FeeManager settings
	err = build.Retry(10, 100*time.Millisecond, func() error {
		err = m.MineBlock()
		if err != nil {
			return err
		}
		wg, err = r.WalletGet()
		if err != nil {
			return err
		}
		if wg.ConfirmedSiacoinBalance.Cmp(devStartingBalance) <= 0 {
			return fmt.Errorf("Expected Dev wallet balance %v to be larger than the starting balance %v", wg.ConfirmedSiacoinBalance.HumanString(), devStartingBalance.HumanString())
		}
		wg, err = fm.WalletGet()
		if err != nil {
			return err
		}
		if wg.ConfirmedSiacoinBalance.Cmp(fmStartingBalance) >= 0 {
			return fmt.Errorf("Expected FeeManager wallet balance %v to be less than the starting balance %v", wg.ConfirmedSiacoinBalance.HumanString(), fmStartingBalance.HumanString())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
