package feemanager

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/types"
)

// TestPersistTransactionCreate test the callPersistTxnCreated method
func TestPersistTransactionCreated(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create fee manager
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Add a random number of fees
	feeUIDs, err := addRandomFeesN(fm, fastrand.Intn(100)+1)
	if err != nil {
		t.Fatal(err)
	}

	// Submit a persist event for a transaction being created
	txn := types.Transaction{}
	err = fm.staticCommon.staticPersist.callPersistTransaction(txn)
	if err != nil {
		t.Fatal(err)
	}
	txnID := txn.ID()
	err = fm.staticCommon.staticPersist.callPersistTxnCreated(feeUIDs, txnID)
	if err != nil {
		t.Fatal(err)
	}

	// Close the FeeManager
	err = fm.Close()
	if err != nil {
		t.Fatal(err)
	}

	// Reopen the FeeManager to load the persistence
	fm, err = New(fm.staticCommon.staticCS, fm.staticCommon.staticTpool, fm.staticCommon.staticWallet, fm.staticCommon.staticPersist.staticPersistDir)
	if err != nil {
		t.Fatal(err)
	}

	// Verify all the fees are now marked as TransactionCreate true
	fm.mu.Lock()
	for _, fee := range fm.fees {
		if !fee.TransactionCreated {
			t.Fatal("fee found with TransactionCreate False")
		}
	}
	fm.mu.Unlock()

	ttxns := fm.staticCommon.staticWatchdog.managedTrackedTxns()
	if len(ttxns) != 1 {
		t.Fatal("Expected 1 tracked transaction but got", len(ttxns))
	}
}
