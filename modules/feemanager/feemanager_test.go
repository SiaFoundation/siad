package feemanager

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	// "gitlab.com/NebulousLabs/fastrand"
)

// TestFeeManagerBasic checks to make sure the creating and closing a FeeManager
// performs as expected and that loading the persistence from disk is as
// expected
func TestFeeManagerBasic(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create FeeManager.
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Close FeeManager.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Re-open the fee manager.
	fm, err = New(fm.common.staticCS, fm.common.staticWallet, fm.common.persist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Add a fee to the fee manager.
	uh := types.UnlockHash{1, 2, 3}
	amount := types.NewCurrency64(100)
	appuid := modules.AppUID("testapp")
	recurring := false
	err = fm.AddFee(uh, amount, appuid, recurring)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the fee is available from the fee manager.
	pendingFees, err := fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 1 {
		t.Fatal("there should be a pending fee")
	}
	pf := pendingFees[0]
	if pf.Address != uh {
		t.Fatal("mismatch")
	}
	if !pf.Amount.Equals(amount) {
		t.Fatal("mismatch")
	}
	if pf.AppUID != appuid {
		t.Fatal("mismatch")
	}
	if pf.PaymentCompleted {
		t.Fatal("unexpected")
	}
	if pf.PayoutHeight == 0 {
		t.Fatal("payout height is too fast")
	}
	if pf.Recurring != recurring {
		t.Fatal("mismatch")
	}
	if pf.Timestamp.Unix() == (time.Time{}).Unix() {
		t.Fatal("timestamp not set")
	}
	if pf.TransactionCreated {
		t.Fatal("unexpected")
	}
	if pf.UID == "" {
		t.Fatal("unset")
	}

	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
}

/*
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
	err = addRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}

	// Get the Fees
	paidFees, err := fm.PaidFees()
	if err != nil {
		t.Fatal(err)
	}
	pendingFees, err := fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}

	// Verify all the fees were set
	originalNumFees := len(pendingFees)
	if originalNumFees != len(fm.fees) {
		t.Fatalf("Not all fees recorded, expected %v pending fees but found %v", originalNumFees, len(fm.fees))
	}
	if len(paidFees) != 0 {
		t.Fatalf("Shouldn't have any paid fees but found %v", len(paidFees))
	}

	// Cancel a random fee
	i := fastrand.Intn(originalNumFees)
	canceledUID := pendingFees[i].UID
	err = fm.CancelFee(canceledUID)
	if err != nil {
		t.Fatal(err)
	}

	// Get the Fees
	paidFees, err = fm.PaidFees()
	if err != nil {
		t.Fatal(err)
	}
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}

	// Verify the number of fees
	if _, ok := fm.fees[canceledUID]; ok {
		t.Fatal("Fee not removed from the map")
	}
	if originalNumFees-1 != len(fm.fees) {
		t.Fatalf("Expected %v fees in the map but found %v", originalNumFees-1, len(fm.fees))
	}
	if originalNumFees-1 != len(pendingFees) {
		t.Fatalf("Expected %v pending fees but found %v", originalNumFees-1, len(pendingFees))
	}
	if len(paidFees) != 0 {
		t.Fatalf("Shouldn't have any paid fees but found %v", len(paidFees))
	}

	// Check the number of Fees in the Fees Persist File
	persistedFees, err := fm.callLoadAllFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(persistedFees) != originalNumFees {
		t.Fatalf("Expected %v fees to be persisted but found %v", originalNumFees, len(persistedFees))
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
	if originalNumFees-1 != len(fm2.fees) {
		t.Fatalf("Expected %v fees in the map but found %v", originalNumFees-1, len(fm2.fees))
	}
}
*/
