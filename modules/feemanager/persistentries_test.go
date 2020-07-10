package feemanager

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestApplyEntry tests the applyEntry method of the FeeManager
func TestApplyEntry(t *testing.T) {
	t.Parallel()
	// Create minimum FeeManager
	fm := &FeeManager{
		fees: make(map[modules.FeeUID]*modules.AppFee),
		staticCommon: &feeManagerCommon{
			staticPersist: &persistSubsystem{
				partialTxns: make(map[types.TransactionID]partialTransactions),
			},
			staticWatchdog: &watchdog{
				feeUIDToTxnID: make(map[modules.FeeUID]types.TransactionID),
				txns:          make(map[types.TransactionID]trackedTransaction),
			},
		},
	}

	// Test bad specifier case
	entry := persistEntry{
		EntryType: types.Specifier{'x'},
	}
	copy(entry.Payload[:], fastrand.Bytes(persistEntrySize))
	encodedEntry := encoding.Marshal(entry)
	err := fm.applyEntry(encodedEntry[:])
	if err != errUnrecognizedEntryType {
		t.Fatalf("Expected error to be %v but was %v", errUnrecognizedEntryType, err)
	}

	// Test random data cases
	err = fm.applyEntry(fastrand.Bytes(100)[:])
	if err == nil {
		t.Fatal("Shouldn't be able to apply random data")
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

	// Check Add Entry
	err = checkApplyAddEntry(fm, fee)
	if err != nil {
		t.Error(err)
	}

	// Check Update Entry
	err = checkApplyUpdateEntry(fm, fee.FeeUID)
	if err != nil {
		t.Error(err)
	}

	// Check Cancel Fee Entry
	err = checkApplyCancelEntry(fm, fee.FeeUID)
	if err != nil {
		t.Error(err)
	}
}

// checkApplyAddEntry is a helper for creating and applying an AddFeeEntry to
// the FeeManager and checking the result.
func checkApplyAddEntry(fm *FeeManager, fee modules.AppFee) error {
	// Call createAddFeeEntry
	pe := createAddFeeEntry(fee)

	// Call applyEntry
	fm.mu.Lock()
	err := fm.applyEntry(pe[:])
	fm.mu.Unlock()
	if err != nil {
		return err
	}

	// Should see the one entry
	fm.mu.Lock()
	numFess := len(fm.fees)
	fm.mu.Unlock()
	if numFess != 1 {
		return fmt.Errorf("Expected 1 fee in FeeManager, found %v", numFess)
	}
	return nil
}

// checkApplyCancelEntry is a helper for creating and applying a CancelFeeEntry
// to the FeeManager and checking the result.
func checkApplyCancelEntry(fm *FeeManager, feeUID modules.FeeUID) error {
	// call createCancelFeeEntry
	pe := createCancelFeeEntry(feeUID)

	// Call applyEntry
	fm.mu.Lock()
	err := fm.applyEntry(pe[:])
	fm.mu.Unlock()
	if err != nil {
		return err
	}

	// Should see no entries
	fm.mu.Lock()
	numFess := len(fm.fees)
	fm.mu.Unlock()
	if numFess != 0 {
		return fmt.Errorf("Expected 0 fees in FeeManager, found %v", numFess)
	}
	return nil
}

// checkApplyUpdateEntry is a helper for creating and applying an UpdateFeeEntry
// to the FeeManager and checking the result.
func checkApplyUpdateEntry(fm *FeeManager, feeUID modules.FeeUID) error {
	// Since the PayoutHeight was set to 0 this fee would need to be updated.
	// Set the payoutheight and reapply
	payoutHeight := types.BlockHeight(fastrand.Uint64n(100))
	pe := createUpdateFeeEntry(feeUID, payoutHeight)
	fm.mu.Lock()
	err := fm.applyEntry(pe[:])
	fm.mu.Unlock()
	if err != nil {
		return err
	}

	// There should still just be the one entry and the payout height should be
	// updated
	fm.mu.Lock()
	numFess := len(fm.fees)
	fm.mu.Unlock()
	if numFess != 1 {
		return fmt.Errorf("Expected 1 fee in FeeManager, found %v", numFess)
	}
	fm.mu.Lock()
	mapFee, ok := fm.fees[feeUID]
	fm.mu.Unlock()
	if !ok {
		return errors.New("Fee not found in map")
	}
	if mapFee.PayoutHeight != payoutHeight {
		return fmt.Errorf("Expected fee in map to have PayoutHeight of %v but was %v", payoutHeight, mapFee.PayoutHeight)
	}
	return nil
}

// TestPersistEntryPayloadSize ensures that the payload size plus the size of
// the rest of the persist entry matches up to the persistEntrySize.
func TestPersistEntryPayloadSize(t *testing.T) {
	t.Parallel()
	var pe persistEntry
	data := encoding.Marshal(pe)
	if len(data) != persistEntrySize {
		t.Fatal("encoded persistEntry must be persistEntrySize")
	}

	// In a loop test random data. Not checking output as this is purely testing
	// that the build.Critical is never hit
	var feeUIDs []modules.FeeUID
	for i := 0; i < 100; i++ {
		txnID := types.TransactionID(crypto.HashBytes(fastrand.Bytes(16)))
		_ = createAddFeeEntry(randomFee())
		_ = createCancelFeeEntry(uniqueID())
		feeUIDs = append(feeUIDs, uniqueID())
		_, _ = createTxnCreatedEntrys(feeUIDs, txnID)
		_, _ = createTxnConfirmedEntrys(feeUIDs, txnID)
		_, _ = createTxnDroppedEntrys(feeUIDs, txnID)
		_ = createUpdateFeeEntry(uniqueID(), types.BlockHeight(fastrand.Uint64n(1e9)))
	}
}

// TestApplyEntryWatchdog tests the applyEntry method of the FeeManager for
// Watchdog related actions
func TestApplyEntryWatchdog(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create  FeeManager
	fm, err := newTestingFeeManager(t.Name())
	if err != nil {
		t.Fatal(err)
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
	err = checkApplyAddEntry(fm, fee)
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
		// Fatal since the next check will panic
		t.Fatal(err)
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
	fm.mu.Lock()
	err := fm.applyEntry(pe[:])
	fm.mu.Unlock()
	if err != nil {
		return err
	}

	// Fee in FeeManager map should show that the transaction was created
	fm.mu.Lock()
	mapFee, ok := fm.fees[feeUID]
	fm.mu.Unlock()
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
	w.mu.Lock()
	tt, ok := w.txns[txnID]
	w.mu.Unlock()
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
		fm.mu.Lock()
		err := fm.applyEntry(pe[:])
		fm.mu.Unlock()
		if err != nil {
			return err
		}
	}

	// Transaction should be in the watchdog
	w := fm.staticCommon.staticWatchdog
	w.mu.Lock()
	numTxns := len(w.txns)
	w.mu.Unlock()
	if numTxns != 1 {
		return fmt.Errorf("Expected %v txns but got %v", 1, numTxns)
	}
	w.mu.Lock()
	_, ok := w.txns[txn.ID()]
	w.mu.Unlock()
	if !ok {
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
		fm.mu.Lock()
		err := fm.applyEntry(pe[:])
		fm.mu.Unlock()
		if err != nil {
			return err
		}
	}

	// Watchdog should have empty maps now
	w.mu.Lock()
	lenFess := len(w.feeUIDToTxnID)
	numTxns := len(w.txns)
	w.mu.Unlock()
	if lenFess != 0 || numTxns != 0 {
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
		fm.mu.Lock()
		err := fm.applyEntry(pe[:])
		fm.mu.Unlock()
		if err != nil {
			return errors.AddContext(err, "unable to apply entry")
		}
	}

	// Watchdog should have empty maps now
	w.mu.Lock()
	lenFess := len(w.feeUIDToTxnID)
	numTxns := len(w.txns)
	w.mu.Unlock()
	if lenFess != 0 || numTxns != 0 {
		return errors.New("watchdog still has transactions")
	}
	return nil
}
