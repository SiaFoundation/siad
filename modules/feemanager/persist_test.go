package feemanager

import (
	"fmt"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

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
		fees: make(map[modules.FeeUID]*modules.AppFee),
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
