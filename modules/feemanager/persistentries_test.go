package feemanager

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestIntegrateEntry tests the integrateEntry method of the FeeManager
func TestIntegrateEntry(t *testing.T) {
	t.Parallel()
	// Create minimum FeeManager
	fm := &FeeManager{
		fees: make(map[modules.FeeUID]*modules.AppFee),
	}

	// Test bad specifier case
	entry := persistEntry{
		EntryType: types.Specifier{'x'},
	}
	copy(entry.Payload[:], fastrand.Bytes(persistEntrySize))
	encodedEntry := encoding.Marshal(entry)
	err := fm.integrateEntry(encodedEntry[:])
	if err != errUnrecognizedEntryType {
		t.Fatalf("Expected error to be %v but was %v", errUnrecognizedEntryType, err)
	}

	// Test random data cases
	err = fm.integrateEntry(fastrand.Bytes(100)[:])
	if err == nil {
		t.Fatal("Shouldn't be able to integrate random data")
	}

	// Create Fee
	fee := modules.AppFee{
		Address:            types.UnlockHash{},
		Amount:             types.NewCurrency64(fastrand.Uint64n(1000)),
		AppUID:             modules.AppUID(uniqueID()),
		PaymentCompleted:   fastrand.Intn(2) == 0,
		PayoutHeight:       0,
		Recurring:          fastrand.Intn(2) == 0,
		Timestamp:          time.Now().Unix(),
		TransactionCreated: fastrand.Intn(2) == 0,
		UID:                uniqueID(),
	}

	// Call createAddFeeEntry
	pe := createAddFeeEntry(fee)

	// Call integrateEntry
	err = fm.integrateEntry(pe[:])
	if err != nil {
		t.Error(err)
	}

	// Should see the one entry
	if len(fm.fees) != 1 {
		t.Errorf("Expected 1 fee in FeeManager, found %v", len(fm.fees))
	}

	// Since the PayoutHeight was set to 0 this fee would need to be updated.
	// Set the payoutheight and reintegrate
	payoutHeight := types.BlockHeight(fastrand.Uint64n(100))
	pe = createUpdateFeeEntry(fee.UID, payoutHeight)
	err = fm.integrateEntry(pe[:])
	if err != nil {
		t.Error(err)
	}

	// There should still just be the one entry and the payout height should be
	// updated
	if len(fm.fees) != 1 {
		t.Errorf("Expected 1 fee in FeeManager, found %v", len(fm.fees))
	}
	mapFee, ok := fm.fees[fee.UID]
	if !ok {
		t.Fatal("Fee not found in map")
	}
	if mapFee.PayoutHeight != payoutHeight {
		t.Errorf("Expected fee in map to have PayoutHeight of %v but was %v", payoutHeight, mapFee.PayoutHeight)
	}

	// createCancelFeeEntry
	pe = createCancelFeeEntry(fee.UID)

	// Call integrateEntry
	err = fm.integrateEntry(pe[:])
	if err != nil {
		t.Error(err)
	}

	// Should see no entries
	if len(fm.fees) != 0 {
		t.Errorf("Expected 0 fees in FeeManager, found %v", len(fm.fees))
	}
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
	for i := 0; i < 1000; i++ {
		_ = createAddFeeEntry(randomFee())
		_ = createCancelFeeEntry(uniqueID())
		_ = createUpdateFeeEntry(uniqueID(), types.BlockHeight(fastrand.Uint64n(1e9)))
	}
}
