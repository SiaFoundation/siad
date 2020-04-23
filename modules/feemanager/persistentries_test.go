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
	err := fm.integrateEntry(pe[:])
	if err != nil {
		t.Error(err)
	}

	// Should see the one entry
	if len(fm.fees) != 1 {
		t.Errorf("Expected 1 fee in FeeManager, found %v", len(fm.fees))
	}

	// Since the PayoutHeight was set to 0 this fee would need to be updated.
	// Set the payoutheight and reintegrate
	fee.PayoutHeight = types.BlockHeight(fastrand.Uint64n(100))
	pe = createAddFeeEntry(fee)
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
	if mapFee.PayoutHeight != fee.PayoutHeight {
		t.Errorf("Expected fee in map to have PayoutHeight of %v but was %v", fee.PayoutHeight, mapFee.PayoutHeight)
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
}
