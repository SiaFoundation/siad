package feemanager

import (
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"

	"gitlab.com/NebulousLabs/fastrand"
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
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Add a fee to the fee manager.
	uh := types.UnlockHash{1, 2, 3}
	amount := types.NewCurrency64(100)
	appuid := modules.AppUID("testapp")
	recurring := false
	_, err = fm.AddFee(uh, amount, appuid, recurring)
	if err != nil {
		t.Fatal(err)
	}

	// Create a function to check this fee for expected values.
	feeCheck := func(af modules.AppFee) {
		if af.Address != uh {
			t.Fatalf("Expected address to be %v but was %v", uh, af.Address)
		}
		if !af.Amount.Equals(amount) {
			t.Fatalf("Expected amount to be %v but was %v", amount.HumanString(), af.Amount.HumanString())
		}
		if af.AppUID != appuid {
			t.Fatalf("Expected appuid to be %v but was %v", appuid, af.AppUID)
		}
		if af.PaymentCompleted {
			t.Fatal("PaymentCompleted should be false")
		}
		if af.PayoutHeight == 0 {
			t.Fatal("payout height is 0")
		}
		if af.Recurring != recurring {
			t.Fatalf("Expected recurring to be %v but was %v", recurring, af.Recurring)
		}
		if af.Timestamp == (time.Time{}).Unix() {
			t.Fatal("timestamp not set")
		}
		if af.TransactionCreated {
			t.Fatal("TransactionCreated should be false")
		}
		if af.FeeUID == "" {
			t.Fatal("FeeUID is blank")
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
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
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
	err = fm.CancelFee(pf.FeeUID)
	if err != nil {
		t.Fatal(err)
	}
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 0 {
		t.Fatal("fee not cancelled")
	}
	// Restart the fee manager.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the fee remains cancelled after startup.
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 0 {
		t.Fatal("fee not cancelled")
	}

	// Add a random number of fees.
	_, err = addRandomFees(fm)
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
		err = fm.CancelFee(fee.FeeUID)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Verify they are cancelled in Memory
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 0 {
		t.Fatalf("Expected 0 fees but found %v", 0)
	}

	// Restart the fee manager and check that all fees are cancelled.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Check the fee again, values should be identical to before.
	pendingFees, err = fm.PendingFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(pendingFees) != 0 {
		t.Fatalf("Expected 0 fees but found %v", 0)
	}
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
}

// TestFeeManagerSyncCoordinator is a large concurrency test on the sync
// coordinator to make sure that the concurrency around adding and removing fees
// is working correctly.
func TestFeeManagerSyncCoordinator(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create FeeManager.
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create a list of fee uids to track what fees are supposed to be in the
	// fee manager.
	var feesMu sync.Mutex
	var fees []modules.FeeUID

	// Create a helper function to add a random fee.
	addRandFee := func() {
		// Add the fee.
		uid, err := addRandomFee(fm)
		if err != nil {
			t.Error(err)
		}
		feesMu.Lock()
		fees = append(fees, uid)
		feesMu.Unlock()
	}

	// Create a helper function to remove a random fee.
	deleteRandFee := func() {
		// Grab a random fee to erase.
		feesMu.Lock()
		if len(fees) == 0 {
			feesMu.Unlock()
			return
		}
		i := fastrand.Intn(len(fees))
		uid := fees[i]
		fees[i] = ""
		feesMu.Unlock()

		// This fee has already been erased, don't bother erasing anything.
		if uid == "" {
			return
		}

		// Delete this fee.
		err := fm.CancelFee(uid)
		if err != nil {
			t.Error(err)
		}
	}

	// Do 10 separate rounds of spinning up and spinning down a large number of
	// goroutines. This stresses the sync coordinator's code which ensures only
	// one syncing thread is running at a time.
	for x := 0; x < 10; x++ {
		// Kick off 40 goroutines to loop over and randomly add and delete fees.
		// This stresses the syncing thread.
		var wg sync.WaitGroup
		for i := 0; i < 40; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 1e3; j++ {
					if fastrand.Intn(4) == 0 {
						deleteRandFee()
					} else {
						addRandFee()
					}
				}
			}()
		}
		wg.Wait()
	}

	// Define helper for all the fee checks
	checkAllFees := func() {
		// Check that the fee manager has exactly the set of fees that it is
		// supposed to.
		allFees, err := fm.PendingFees()
		if err != nil {
			t.Fatal(err)
		}
		feeMap := make(map[modules.FeeUID]struct{})
		recentTime := allFees[0].Timestamp
		recentTime--
		for _, fee := range allFees {
			// Check that the fees are sorted.
			if fee.Timestamp < recentTime {
				t.Error("bad sorting")
			}
			recentTime = fee.Timestamp

			// Check that this fee is not already in the feeMap.
			_, exists := feeMap[fee.FeeUID]
			if exists {
				t.Error("double fee")
			}
			feeMap[fee.FeeUID] = struct{}{}
		}
		// Check that every fee in our uid list appears in the fee map.
		var totalFees int
		for _, uid := range fees {
			// Skip deleted fees.
			if uid == "" {
				continue
			}
			totalFees++
			_, exists := feeMap[uid]
			if !exists {
				t.Error("missing fee")
			}
		}
		// Check that the total number of fees in the list is the same as in the fm.
		if len(allFees) != totalFees || len(feeMap) != totalFees {
			t.Log("allFees:", len(allFees))
			t.Log("feeMap:", len(feeMap))
			t.Log("totalFees:", totalFees)
			t.Error("wrong fee count")
		}
	}

	// Check that the fee manager has exactly the set of fees that it is
	// supposed to.
	checkAllFees()

	// Restart the fee manger and check that it still has exactly the set of
	// fees that it is supposed to.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the fee manager has exactly the set of fees that it is
	// supposed to.
	checkAllFees()

	// Close FeeManager.
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}
}
