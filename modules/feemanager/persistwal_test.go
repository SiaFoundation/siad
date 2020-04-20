package feemanager

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TestApplyWalUpdates tests a variety of functions that are used to apply wal
// updates.
func TestApplyWalUpdates(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("TestApplyUpdates", func(t *testing.T) {
		fm, err := newTestingFeeManager(t.Name())
		if err != nil {
			t.Fatal(err)
		}
		testApply(t, fm, fm.applyUpdates)
	})
	t.Run("TestCreateAndApplyTransaction", func(t *testing.T) {
		fm, err := newTestingFeeManager(t.Name())
		if err != nil {
			t.Fatal(err)
		}
		testApply(t, fm, fm.createAndApplyTransaction)
	})
}

// testApply tests if a given method applies a set of updates correctly.
func testApply(t *testing.T, fm *FeeManager, apply func(...writeaheadlog.Update) error) {
	// Create an append update and a persist update
	persist, err := randomPersistence()
	if err != nil {
		t.Fatal(err)
	}
	fees := persist.Fees
	persistUpdate, err := createPersistUpdate(persist)
	if err != nil {
		t.Fatal(err)
	}
	updates := []writeaheadlog.Update{persistUpdate}
	for _, fee := range fees {
		update, err := createInsertUpdate(fee)
		if err != nil {
			t.Fatal(err)
		}
		updates = append(updates, update)
	}

	// Apply update.
	if err := apply(updates...); err != nil {
		t.Fatal("Failed to apply updates", err)
	}

	// Check the FeeManager Persist File
	err = fm.load()
	if err != nil {
		t.Fatal(err)
	}
	err = verifyLoadedPersistence(fm, persist)
	if err != nil {
		t.Fatal(err)
	}

	// Check the Fee Persist File
	readFees, err := fm.callLoadAllFees()
	if err != nil {
		t.Fatal(err)
	}
	if len(readFees) != len(fees) {
		t.Fatalf("Expected %v fees but found %v", len(fees), len(readFees))
	}
	if !reflect.DeepEqual(fees, readFees) {
		t.Logf("Initial Fees: %v", fees)
		t.Logf("Read Fees: %v", readFees)
		t.Fatal("Fees not equal")
	}
}

// TestCreateAndReadUpdates probes the create and read update functions
func TestCreateAndReadUpdates(t *testing.T) {
	// Create Persistence and Fees
	persist, err := randomPersistence()
	if err != nil {
		t.Fatal(err)
	}
	fees := persist.Fees

	// Test the insert updates
	for _, fee := range fees {
		// Create Insert Updates
		update, err := createInsertUpdate(fee)
		if err != nil {
			t.Fatal(err)
		}

		// Check the update
		if update.Name != updateNameInsert {
			t.Fatalf("Expected update name to be %v but was %v", updateNameInsert, update.Name)
		}

		// Read Insert Update
		data, offset, err := readInsertUpdate(update)
		if err != nil {
			t.Fatal(err)
		}
		if offset != fee.Offset {
			t.Fatalf("Unexpected Offset; have %v expected %v", offset, fee.Offset)
		}

		// Check the read data
		readFees, err := unmarshalFees(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(readFees) != 1 {
			t.Fatalf("Expected 1 fee but found %v", len(readFees))
		}
		if !reflect.DeepEqual(fee, readFees[0]) {
			t.Logf("Initial Fee: %v", fee)
			t.Logf("Read Fee: %v", readFees[0])
			t.Fatal("Fees not equal")
		}
	}

	// Create Persist UPdate
	update, err := createPersistUpdate(persist)
	if err != nil {
		t.Fatal(err)
	}

	// Check the update
	if update.Name != updateNamePersist {
		t.Fatalf("Expected update name to be %v but was %v", updateNamePersist, update.Name)
	}

	// Check the read data
	data := update.Instructions
	var readPersist persistence
	err = json.Unmarshal(data, &readPersist)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(persist, readPersist) {
		t.Logf("Initial persist: %v", persist)
		t.Logf("Read persist: %v", readPersist)
		t.Fatal("Persist not equal")
	}
}

// TestIsFeeManagerUpdate probes the isFeeManagerUpdate method
func TestIsFeeManagerUpdate(t *testing.T) {
	var updateTests = []struct {
		updateName string
		valid      bool
	}{
		{updateNameInsert, true},
		{updateNamePersist, true},
		{"feeManagerAppendFee", false},
		{"FeemanagerPersist", false},
		{"random", false},
	}

	for _, test := range updateTests {
		update := writeaheadlog.Update{
			Name: test.updateName,
		}
		if test.valid != isFeeManagerUpdate(update) {
			t.Errorf("%v should have be valid %v", test.updateName, test.valid)
		}
	}
}

// randomFee returns a random appFee
//
// NOTE: This random information is intended to test edge cases, values like
// offset and cancelled should be expected to not line up with normal operating
// conditions
func randomFee() appFee {
	return appFee{
		Address:      types.UnlockHash{},
		Amount:       types.NewCurrency64(fastrand.Uint64n(100)),
		AppUID:       modules.AppUID(uniqueID()),
		Cancelled:    fastrand.Intn(100)%2 == 0,
		Offset:       int64(fastrand.Intn(1000)),
		PayoutHeight: types.BlockHeight(fastrand.Uint64n(100)),
		Recurring:    fastrand.Intn(100)%2 == 0,
		UID:          uniqueID(),
	}
}

// randomFees returns a random number for fees between 0-4. It ensures the
// offset as valid and returns the next offset
func randomFees() ([]appFee, int64, error) {
	var fees []appFee
	var nextOffset int64
	for i := 0; i < fastrand.Intn(5); i++ {
		fee := randomFee()
		fee.Offset = nextOffset
		var buf bytes.Buffer
		err := fee.marshalSia(&buf)
		if err != nil {
			return []appFee{}, 0, err
		}
		nextOffset += int64(buf.Len())
		fees = append(fees, fee)
	}
	return fees, nextOffset, nil
}

// randomPersistence returns a persistence struct filled in with random info
func randomPersistence() (persistence, error) {
	fees, offset, err := randomFees()
	if err != nil {
		return persistence{}, err
	}
	return randomPersistenceWithFees(fees, offset), nil
}

// randomPersistence returns a persistence struct filled in with the provided
// fees and random info.
func randomPersistenceWithFees(fees []appFee, offset int64) persistence {
	return persistence{
		NextFeeOffset: offset,
		PayoutHeight:  types.BlockHeight(fastrand.Uint64n(1000)),
		Fees:          fees,
	}
}
