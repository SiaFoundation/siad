package feemanager

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestApplyEntryWatchdog tests the applyEntry method of the FeeManager for
// Watchdog related actions
func TestApplyEntryWatchdog(t *testing.T) {
	t.Parallel()
	// Create minimum FeeManager
	fm := &FeeManager{
		fees: make(map[modules.FeeUID]*modules.AppFee),
		staticCommon: &feeManagerCommon{
			staticWatchdog: &watchdog{
				feeUIDToTxnID: make(map[modules.FeeUID]types.TransactionID),
				partialTxns:   make(map[types.TransactionID]partialTransactions),
				txns:          make(map[types.TransactionID]trackedTransaction),
			},
		},
	}

	// Create Fee
	fee := modules.AppFee{
		Address:            types.UnlockHash{},
		Amount:             types.NewCurrency64(fastrand.Uint64n(1000)),
		AppUID:             modules.AppUID(uniqueID()),
		PaymentCompleted:   false,
		PayoutHeight:       0,
		Recurring:          fastrand.Intn(2) == 0,
		Timestamp:          time.Now().Unix(),
		TransactionCreated: false,
		FeeUID:             uniqueID(),
	}

	// Add a fee to the feemanager
	err := checkApplyAddEntry(fm, fee)
	if err != nil {
		t.Error(err)
	}

	// Check Transaction Entry. Create transaction with arbitrary data to test
	// transactions of varying size
	txn := types.Transaction{}
	txn.ArbitraryData = [][]byte{fastrand.Bytes(fastrand.Intn(2 * persistTransactionSize))}
	err = checkApplyTransactionEntry(fm, txn)
	if err != nil {
		t.Error(err)
	}

	// Check Txn Created Entry
	txnID := txn.ID()
	err = checkApplyTxnCreatedEntry(fm, fee.FeeUID, txnID)
	if err != nil {
		t.Error(err)
	}

	// Check Dropped Transaction entry
	feeUIDs := []modules.FeeUID{fee.FeeUID}
	err = checkApplyTxnDroppedEntry(fm, feeUIDs, txnID)
	if err != nil {
		t.Error(err)
	}

	// Add the transaction again
	err = checkApplyTransactionEntry(fm, txn)
	if err != nil {
		t.Error(err)
	}
	err = checkApplyTxnCreatedEntry(fm, fee.FeeUID, txnID)
	if err != nil {
		t.Error(err)
	}

	// Check Txn Confirmed Entry
	err = checkApplyTxnConfirmedEntry(fm, feeUIDs, txnID)
	if err != nil {
		t.Error(err)
	}
}

// checkApplyTxnCreatedEntry is a helper for creating and applying a
// TxnCreatedEntry to the FeeManager and checking the result.
func checkApplyTxnCreatedEntry(fm *FeeManager, feeUID modules.FeeUID, txnID types.TransactionID) error {
	// createTxnCreatedEntry with 1 feeUID should only create 1 persist entry
	pes, _ := createTxnCreatedEntrys([]modules.FeeUID{feeUID}, txnID)
	pe := pes[0]

	// Call applyEntry
	err := fm.applyEntry(pe[:])
	if err != nil {
		return err
	}

	// Fee in FeeManager map should show that the transaction was created
	mapFee, ok := fm.fees[feeUID]
	if !ok {
		return errors.New("Fee not found in map")
	}
	if !mapFee.TransactionCreated {
		return errors.New("Expected TransactionCreate to be true")
	}

	w := fm.staticCommon.staticWatchdog
	// FeeUID and TxnID should now be in the watchdog
	if len(w.txns) != 1 {
		return fmt.Errorf("Expected %v entry in ID map but got %v", 1, len(w.txns))
	}
	tt, ok := w.txns[txnID]
	if !ok {
		return errors.New("transaction not found in watchdog ID map")
	}
	if len(tt.feeUIDs) != 1 {
		return fmt.Errorf("Expected 1 feeUID but got %v", len(tt.feeUIDs))
	}
	if tt.feeUIDs[0] != mapFee.FeeUID {
		return fmt.Errorf("Wrong fee in watchdog, expected %v, got %v", mapFee.FeeUID, tt.feeUIDs[0])
	}
	return nil
}

// checkApplyTransactionEntry is a helper for creating and applying a
// TransactionEntry to the FeeManager and checking the result.
func checkApplyTransactionEntry(fm *FeeManager, txn types.Transaction) error {
	pes, err := createTransactionEntrys(txn)
	if err != nil {
		return errors.AddContext(err, "unable to create transaction entries")
	}

	// Call applyEntry
	for _, pe := range pes {
		err := fm.applyEntry(pe[:])
		if err != nil {
			return err
		}
	}

	// Transaction should be in the watchdog
	w := fm.staticCommon.staticWatchdog
	numTxns := len(w.txns)
	if numTxns != 1 {
		return fmt.Errorf("Expected %v txns but got %v", 1, numTxns)
	}
	if _, ok := w.txns[txn.ID()]; !ok {
		return errors.New("transaction not found in watchdog")
	}
	return nil
}

// checkApplyTxnConfirmedEntry is a helper for creating and applying a
// TxnConfirmedEntry to the FeeManager and checking the result.
func checkApplyTxnConfirmedEntry(fm *FeeManager, feeUIDs []modules.FeeUID, txnID types.TransactionID) error {
	w := fm.staticCommon.staticWatchdog

	// createTxnConfirmedEntrys
	pes, err := createTxnConfirmedEntrys(feeUIDs, txnID)
	if err != nil {
		return errors.AddContext(err, "unable to create transaction confirmed entries")
	}

	// Call applyEntry
	for _, pe := range pes {
		err := fm.applyEntry(pe[:])
		if err != nil {
			return err
		}
	}

	// Watchdog should have empty maps now
	if len(w.txns) != 0 || len(w.feeUIDToTxnID) != 0 {
		return errors.New("watchdog still has transactions")
	}
	return nil
}

// checkApplyTxnDroppedEntry is a helper for creating and applying a
// TxnDroppedEntry to the FeeManager and checking the result.
func checkApplyTxnDroppedEntry(fm *FeeManager, feeUIDs []modules.FeeUID, txnID types.TransactionID) error {
	w := fm.staticCommon.staticWatchdog

	// createTxnDroppedEntrys
	pes, err := createTxnDroppedEntrys(feeUIDs, txnID)
	if err != nil {
		return errors.AddContext(err, "unable to create transaction dropped entries")
	}

	// Call applyEntry
	for _, pe := range pes {
		err := fm.applyEntry(pe[:])
		if err != nil {
			return err
		}
	}

	// Watchdog should have empty maps now
	if len(w.txns) != 0 || len(w.feeUIDToTxnID) != 0 {
		return errors.New("watchdog still has transactions")
	}
	return nil
}
