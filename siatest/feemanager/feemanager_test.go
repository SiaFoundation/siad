package feemanager

import (
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node"
	"gitlab.com/NebulousLabs/Sia/siatest"
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

	// Get the FeeManager.
	fmg, err := fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}

	// There should be no fees associated with the FeeManager
	if len(fmg.PendingFees) != 0 {
		t.Errorf("Expected 0 PendingFees but got %v", len(fmg.PendingFees))
	}
	if len(fmg.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmg.PaidFees))
	}
	if !fmg.Settings.CurrentPayout.IsZero() {
		t.Errorf("Current Payout should be zero but was %v", fmg.Settings.CurrentPayout.HumanString())
	}

	// Set a Fee
	amount := types.NewCurrency64(1000)
	address := types.UnlockHash{}
	appUID := modules.AppUID("testapp")
	recurring := false
	err = fm.FeeManagerSetPost(address, amount, appUID, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Check for Fee
	fmg, err = fm.FeeManagerGet()
	if err != nil {
		t.Fatal(err)
	}
	if len(fmg.PendingFees) != 1 {
		t.Errorf("Expected 1 PendingFee but got %v", len(fmg.PendingFees))
	}
	if len(fmg.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmg.PaidFees))
	}
	if fmg.Settings.CurrentPayout.Cmp(amount) != 0 {
		t.Errorf("Current Payout should be %v but was %v", amount.HumanString(), fmg.Settings.CurrentPayout.HumanString())
	}

	fee := fmg.PendingFees[0]
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
	if len(fmg.PendingFees) != 0 {
		t.Errorf("Expected 0 PendingFees but got %v", len(fmg.PendingFees))
	}
	if len(fmg.PaidFees) != 0 {
		t.Errorf("Expected 0 PaidFees but got %v", len(fmg.PaidFees))
	}
	if !fmg.Settings.CurrentPayout.IsZero() {
		t.Errorf("Current Payout should be zero but was %v", fmg.Settings.CurrentPayout.HumanString())
	}
}

// TestFeeManagerFeeProcessing probes the processing of the fees by the
// FeeManager
//
//  TODO - set dependency to manually set the nebulous address
func TestFeeManagerFeeProcessing(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Skip("Not yet implemented")

	// Create a new FeeManager
	//
	// TODO - feemanager should be created with dependency that allows for the
	// manual setting of the nebulous payout address

	// create 2 other nodes and fully connect them, one node should be the
	// developer wallet and the other should represent nebulous's wallet

	// Set two Fees with the developer wallet's address. one recurring and one
	// not

	// Check for Fees and FeeManager settings

	// Mine blocks to trigger the first payout period

	// Check for Wallets, Fees, and FeeManager settings

	// Mine blocks to trigger the Second payout period

	// Check for Wallets, Fees, and FeeManager settings
}
