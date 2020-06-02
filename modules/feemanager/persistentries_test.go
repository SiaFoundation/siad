package feemanager

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestApplyEntry tests the applyEntry method of the FeeManager
func TestApplyEntry(t *testing.T) {
	t.Parallel()
	// Create minimum FeeManager
	fm := &FeeManager{
		fees:         make(map[modules.FeeUID]*modules.AppFee),
		staticCommon: &feeManagerCommon{},
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

	testApplyEntryFeemanager(t, fm, fee)
}

// testApplyEntryFeemanager handles testing the apply entry calls that are
// focused on updates to the FeeManager
func testApplyEntryFeemanager(t *testing.T, fm *FeeManager, fee modules.AppFee) {
	// Check Add Entry
	err := checkApplyAddEntry(fm, fee)
	if err != nil {
		t.Error(err)
	}

	// Check Update Entry
	err = checkApplyUpdateEntry(fm, fee.FeeUID)
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
	err := fm.applyEntry(pe[:])
	if err != nil {
		return err
	}

	// Should see the one entry
	if len(fm.fees) != 1 {
		return fmt.Errorf("Expected 1 fee in FeeManager, found %v", len(fm.fees))
	}
	return nil
}

// checkApplyCancelEntry is a helper for creating and applying a CancelFeeEntry
// to the FeeManager and checking the result.
func checkApplyCancelEntry(fm *FeeManager, feeUID modules.FeeUID) error {
	// createCancelFeeEntry
	pe := createCancelFeeEntry(feeUID)

	// Call applyEntry
	err := fm.applyEntry(pe[:])
	if err != nil {
		return err
	}

	// Should see no entries
	if len(fm.fees) != 0 {
		return fmt.Errorf("Expected 0 fees in FeeManager, found %v", len(fm.fees))
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

	// TODO - Transaction should be in the watchdog
	return nil
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

	// Fee in FeeManager App should show that the transaction was created
	mapFee, ok := fm.fees[feeUID]
	if !ok {
		return errors.New("Fee not found in map")
	}
	if !mapFee.TransactionCreated {
		return errors.New("Expected TransactionCreate to be true")
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
	err := fm.applyEntry(pe[:])
	if err != nil {
		return err
	}

	// There should still just be the one entry and the payout height should be
	// updated
	if len(fm.fees) != 1 {
		return fmt.Errorf("Expected 1 fee in FeeManager, found %v", len(fm.fees))
	}
	mapFee, ok := fm.fees[feeUID]
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
		_ = createUpdateFeeEntry(uniqueID(), types.BlockHeight(fastrand.Uint64n(1e9)))
	}
}
