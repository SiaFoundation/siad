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

	// Create a function to check this fee for expected values.
	feeCheck := func(af modules.AppFee) {
		if af.Address != uh {
			t.Fatal("mismatch")
		}
		if !af.Amount.Equals(amount) {
			t.Fatal("mismatch")
		}
		if af.AppUID != appuid {
			t.Fatal("mismatch")
		}
		if af.PaymentCompleted {
			t.Fatal("unexpected")
		}
		if af.PayoutHeight == 0 {
			t.Fatal("payout height is too fast")
		}
		if af.Recurring != recurring {
			t.Fatal("mismatch")
		}
		if af.Timestamp == (time.Time{}).Unix() {
			t.Fatal("timestamp not set")
		}
		if af.TransactionCreated {
			t.Fatal("unexpected")
		}
		if af.UID == "" {
			t.Fatal("unset")
		}
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
	feeCheck(pf)

	// Restart the fee manager.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.common.staticCS, fm.common.staticWallet, fm.common.persist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	// Check the fee again, values should be identical to before.
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 1 {
		t.Fatal("there should be a pending fee")
	}
	pf = pendingFees[0]
	feeCheck(pf)

	// Cancel the fee.
	err = fm.CancelFee(pf.UID)
	if err != nil {
		t.Fatal(err)
	}
	pendingFees, err = fm.PendingFees()
	if len(pendingFees) != 0 {
		t.Fatal("fee not cancelled")
	}
	// Restart the fee manager.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.common.staticCS, fm.common.staticWallet, fm.common.persist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the fee remains cancelled after startup.
	pendingFees, err = fm.PendingFees()
	if len(pendingFees) != 0 {
		t.Fatal("fee not cancelled")
	}

	// Add a random number of fees.
	err = addRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}
	// Fetch all the fees and check that they are sorted correctly.
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	recent := pendingFees[0].Timestamp
	for i := 1; i < len(pendingFees); i++ {
		if recent > pendingFees[i].Timestamp {
			t.Error("fees not sorted correctly")
		}
		recent = pendingFees[i].Timestamp
	}
	// Cancel all of the fees.
	for _, fee := range pendingFees {
		err = fm.CancelFee(fee.UID)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Restart the fee manager and check that all fees are cancelled.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.common.staticCS, fm.common.staticWallet, fm.common.persist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	// Check the fee again, values should be identical to before.
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 0 {
		t.Fatal("there should not be any fees")
	}
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
}
