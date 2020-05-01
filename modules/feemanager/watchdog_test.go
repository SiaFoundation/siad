package feemanager

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestWatchdog probes some of the methods of the watchdog
func TestWatchdog(t *testing.T) {
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

	// Create Random Fees.
	feeUIDs, err := addRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}

	// Add a transaction to the watchdog
	w := fm.staticCommon.staticWatchdog
	txn1 := types.Transaction{}
	w.callMonitorTransaction(feeUIDs, txn1)
	ttxns := w.managedTrackedTxns()
	if len(ttxns) != 1 {
		t.Fatalf("Expected one tracked transaction got %v", len(ttxns))
	}
	if len(w.feeUIDToTxnID) != len(feeUIDs) {
		t.Fatalf("Fee to txnID map not updated, expected length of %v got %v", len(feeUIDs), len(w.feeUIDToTxnID))
	}
	ttxn1 := ttxns[0]

	// Add a second transaction to the watchdog so that one of the FeeUIDs is
	// updated to a new TxnID
	txn2 := types.Transaction{}
	txn2.ArbitraryData = [][]byte{fastrand.Bytes(20)}
	singleFeeUID := []modules.FeeUID{feeUIDs[0]}
	w.callMonitorTransaction(singleFeeUID, txn2)
	ttxns = w.managedTrackedTxns()
	if len(ttxns) != 2 {
		t.Fatalf("Expected two tracked transaction got %v", len(ttxns))
	}
	if len(w.feeUIDToTxnID) != len(feeUIDs) {
		t.Fatalf("Fee to txnID map should have the same length, expected length of %v got %v", len(feeUIDs), len(w.feeUIDToTxnID))
	}
	txnID, ok := w.feeUIDToTxnID[singleFeeUID[0]]
	if !ok {
		t.Fatal("FeeUID not found in map")
	}
	if txnID != txn2.ID() {
		t.Fatalf("TxnID for FeeUID in map is incorrect, got %v expected %v", txnID, txn2.ID())
	}

	// Clear the first Transaction, this should only clear the first transaction
	// and all the fees but the one that was updated to the new txnID
	w.managedClearTransaction(ttxn1)
	if len(w.txns) != 1 || len(w.feeUIDToTxnID) != 1 {
		t.Fatal("Watchdog should only be tracking the second transaction and fee", len(w.txns), len(w.feeUIDToTxnID))
	}

	// Reset the watchdog
	w.txns = make(map[types.TransactionID]trackedTransaction)
	w.feeUIDToTxnID = make(map[modules.FeeUID]types.TransactionID)

	// Add the first transaction to the watchdog again
	w.callMonitorTransaction(feeUIDs, txn1)
	ttxns = w.managedTrackedTxns()
	if len(ttxns) != 1 {
		t.Fatalf("Expected one tracked transaction got %v", len(ttxns))
	}

	// Confirm transaction
	err = fm.managedConfirmTransaction(ttxns[0])
	if err != nil {
		t.Fatal(err)
	}

	// Verify all the fees are marked as payment complete
	for _, fee := range fm.fees {
		if !fee.PaymentCompleted {
			t.Error("fee found with PaymentCompleted as false")
		}
	}

	// The Watchdog should also not have a record of any of the fees anymore
	if len(w.txns) != 0 || len(w.feeUIDToTxnID) != 0 {
		t.Fatal("Watchdog is still tracking transactions and fees", len(w.txns), len(w.feeUIDToTxnID))
	}
}
