package feemanager

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAppFeeEncoding probes the encoding of the AppFees
func TestAppFeeEncoding(t *testing.T) {
	// Create fees
	fee1 := appFee{
		Address:   types.UnlockHash{},
		Amount:    types.NewCurrency64(fastrand.Uint64n(100)),
		AppUID:    modules.AppUID(uniqueID()),
		Cancelled: fastrand.Intn(100)%2 == 0,
		Offset:    int64(fastrand.Intn(1000)),
		Recurring: fastrand.Intn(100)%2 == 0,
		UID:       modules.FeeUID("fee1"),
	}
	fee2 := appFee{
		Address:   types.UnlockHash{},
		Amount:    types.NewCurrency64(fastrand.Uint64n(100)),
		AppUID:    modules.AppUID(uniqueID()),
		Cancelled: fastrand.Intn(100)%2 == 0,
		Offset:    int64(fastrand.Intn(1000)),
		Recurring: fastrand.Intn(100)%2 == 0,
		UID:       modules.FeeUID("fee2"),
	}

	// Marshal Fees
	var buf1, buf2 bytes.Buffer
	err := fee1.marshalSia(&buf1)
	if err != nil {
		t.Fatal(err)
	}
	err = fee2.marshalSia(&buf2)
	if err != nil {
		t.Fatal(err)
	}

	// Unmarshal fees
	fees, err := unmarshalFees(append(buf1.Bytes(), buf2.Bytes()...))
	if err != nil {
		t.Fatal(err)
	}

	// Check Fees
	if len(fees) != 2 {
		t.Fatalf("Expected 2 fees but found %v", len(fees))
	}
	if !reflect.DeepEqual(fees[0], fee1) {
		t.Log("Fees Before", fee1)
		t.Log("Fees After", fees[0])
		t.Fatal("Fees not equal after encoding")
	}
	if !reflect.DeepEqual(fees[1], fee2) {
		t.Log("Fees Before", fee2)
		t.Log("Fees After", fees[1])
		t.Fatal("Fees not equal after encoding")
	}
}

// TestLoadPersistData verifies that the persist data is loaded into the
// FeeManager properly
func TestLoadPersistData(t *testing.T) {
	// Create persistence
	persistData, err := randomPersistence()
	if err != nil {
		t.Fatal(err)
	}

	// Load into FeeManager
	fm := &FeeManager{
		fees: make(map[modules.FeeUID]*appFee),
	}
	err = fm.loadPersistData(persistData)
	if err != nil {
		t.Fatal(err)
	}

	// Verify data
	err = verifyLoadedPersistence(fm, persistData)
	if err != nil {
		t.Fatal(err)
	}
}

// verifyLoadedPersistence is a helper function to verify that the persistence
// that is loaded into the FeeManager as expected
func verifyLoadedPersistence(fm *FeeManager, persistData persistence) error {
	// Check individual value fields
	if fm.currentPayout.Cmp(persistData.CurrentPayout) != 0 {
		return fmt.Errorf("Expected currentPayout to be %v but was %v", persistData.CurrentPayout, fm.currentPayout)
	}
	if fm.maxPayout.Cmp(persistData.MaxPayout) != 0 {
		return fmt.Errorf("Expected maxPayout to be %v but was %v", persistData.MaxPayout, fm.maxPayout)
	}
	if fm.payoutHeight != persistData.PayoutHeight {
		return fmt.Errorf("Expected payoutHeight to be %v but was %v", persistData.PayoutHeight, fm.payoutHeight)
	}
	if fm.nextFeeOffset != persistData.NextFeeOffset {
		return fmt.Errorf("Expected nextFeeOffset to be %v but was %v", persistData.NextFeeOffset, fm.nextFeeOffset)
	}

	// Check Fees
	if len(fm.fees) != len(persistData.Fees) {
		return fmt.Errorf("Expected %v fess but found %v", len(persistData.Fees), len(fm.fees))
	}
	for _, fee := range persistData.Fees {
		fmFee, ok := fm.fees[fee.UID]
		if !ok {
			return fmt.Errorf("Fee %v not found in FeeManager", fee.UID)
		}
		if !reflect.DeepEqual(fee, *fmFee) {
			return fmt.Errorf("Fees not equal: Persist Fee %v FeeManager Fee %v", fee, *fmFee)
		}
	}
	return nil
}
