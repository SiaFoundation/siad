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
	defer func() {
		if err := fm.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create Random Fees.
	feeUIDs, err := addRandomFees(fm)
	if err != nil {
		t.Fatal(err)
	}

	// Add a transaction to the watchdog
	w := fm.staticCommon.staticWatchdog
	txn1 := types.Transaction{}
	err = w.callMonitorTransaction(feeUIDs, txn1)
	if err != nil {
		t.Fatal(err)
	}
	ttxns := w.managedTrackedTxns()
	if len(ttxns) != 1 {
		t.Fatalf("Expected one tracked transaction got %v", len(ttxns))
	}
	w.mu.Lock()
	lenFeeMap := len(w.feeUIDToTxnID)
	w.mu.Unlock()
	if lenFeeMap != len(feeUIDs) {
		t.Fatalf("Fee to txnID map not updated, expected length of %v got %v", len(feeUIDs), lenFeeMap)
	}
	ttxn1 := ttxns[0]

	// Add a second transaction to the watchdog so that one of the FeeUIDs is
	// updated to a new TxnID
	txn2 := types.Transaction{}
	txn2.ArbitraryData = [][]byte{fastrand.Bytes(20)}
	singleFeeUID := []modules.FeeUID{feeUIDs[0]}
	_ = w.callMonitorTransaction(singleFeeUID, txn2)
	if err != nil {
		t.Fatal(err)
	}
	ttxns = w.managedTrackedTxns()
	if len(ttxns) != 2 {
		t.Fatalf("Expected two tracked transaction got %v", len(ttxns))
	}
	w.mu.Lock()
	lenFeeMap = len(w.feeUIDToTxnID)
	w.mu.Unlock()
	if lenFeeMap != len(feeUIDs) {
		t.Fatalf("Fee to txnID map should have the same length, expected length of %v got %v", len(feeUIDs), lenFeeMap)
	}
	w.mu.Lock()
	txnID, ok := w.feeUIDToTxnID[singleFeeUID[0]]
	w.mu.Unlock()
	if !ok {
		t.Fatal("FeeUID not found in map")
	}
	if txnID != txn2.ID() {
		t.Fatalf("TxnID for FeeUID in map is incorrect, got %v expected %v", txnID, txn2.ID())
	}

	// Clear the first Transaction, this should only clear the first transaction
	// and all the fees but the one that was updated to the new txnID
	w.managedClearTransaction(ttxn1)
	w.mu.Lock()
	lenFeeMap = len(w.feeUIDToTxnID)
	lenTxnMap := len(w.txns)
	w.mu.Unlock()
	if lenTxnMap != 1 || lenFeeMap != 1 {
		t.Fatal("Watchdog should only be tracking the second transaction and fee", lenTxnMap, lenFeeMap)
	}

	// Reset the watchdog
	w.mu.Lock()
	w.txns = make(map[types.TransactionID]trackedTransaction)
	w.feeUIDToTxnID = make(map[modules.FeeUID]types.TransactionID)
	w.mu.Unlock()

	// Add the first transaction to the watchdog again
	_ = w.callMonitorTransaction(feeUIDs, txn1)
	if err != nil {
		t.Fatal(err)
	}
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
	w.mu.Lock()
	lenFeeMap = len(w.feeUIDToTxnID)
	lenTxnMap = len(w.txns)
	w.mu.Unlock()
	if lenTxnMap != 0 || lenFeeMap != 0 {
		t.Fatal("Watchdog is still tracking transactions and fees", lenTxnMap, lenFeeMap)
	}

	// Call callMonitorTransaction twice. The First should succed so we check the
	// error. The second should build.Critical so we don't check the error but
	// recover in a defer call
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected build critical from calling callMonitorTransaction twice on the same transaction")
		}
	}()
	err = w.callMonitorTransaction(feeUIDs, txn1)
	if err != nil {
		t.Fatal(err)
	}
	_ = w.callMonitorTransaction(feeUIDs, txn1)
}
