package main

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestFilePercentageBreakdown tests the filePercentageBreakdown function
func TestFilePercentageBreakdown(t *testing.T) {
	// Test for Panics
	//
	// Test with empty slice
	var dirs []directoryInfo
	filePercentageBreakdown(dirs)
	// Test with empty struct
	dirs = append(dirs, directoryInfo{})
	filePercentageBreakdown(dirs)

	// Test basic directory info
	dir := modules.DirectoryInfo{AggregateNumFiles: 7}
	f1 := modules.FileInfo{MaxHealthPercent: 100}
	f2 := modules.FileInfo{MaxHealthPercent: 80}
	f3 := modules.FileInfo{MaxHealthPercent: 60}
	f4 := modules.FileInfo{MaxHealthPercent: 30}
	f5 := modules.FileInfo{MaxHealthPercent: 10}
	f6 := modules.FileInfo{MaxHealthPercent: 0, OnDisk: true}
	f7 := modules.FileInfo{MaxHealthPercent: 0}
	files := []modules.FileInfo{f1, f2, f3, f4, f5, f6, f7}
	dirs[0] = directoryInfo{
		dir:   dir,
		files: files,
	}
	fh, g75, g50, g25, g0, ur, err := filePercentageBreakdown(dirs)
	if err != nil {
		t.Fatal(err)
	}

	// Define helper check function
	checkValue := func(expected, actual float64) error {
		if expected != actual {
			return fmt.Errorf("Expected %v, actual %v", expected, actual)
		}
		return nil
	}

	// Check values
	expected := float64(100) * float64(1) / float64(7)
	if err := checkValue(expected, fh); err != nil {
		t.Log("Full Health")
		t.Error(err)
	}
	if err := checkValue(expected, g75); err != nil {
		t.Log("Greater than 75")
		t.Error(err)
	}
	if err := checkValue(expected, g50); err != nil {
		t.Log("Greater than 50")
		t.Error(err)
	}
	if err := checkValue(expected, g25); err != nil {
		t.Log("Greater than 25")
		t.Error(err)
	}
	if err := checkValue(expected, ur); err != nil {
		t.Log("Unrecoverable")
		t.Error(err)
	}
	expected = float64(100) * float64(2) / float64(7)
	if err := checkValue(expected, g0); err != nil {
		t.Log("Greater than 0")
		t.Error(err)
	}
}

// TestContractInfo is a regression test to check for negative currency panics
func TestContractInfo(t *testing.T) {
	contract := api.RenterContract{
		ID:        fileContractID(),
		Fees:      types.NewCurrency64(100),
		TotalCost: types.ZeroCurrency,
	}
	printContractInfo(contract.ID.String(), []api.RenterContract{contract})
}

// fileContractID is a helper function for generating a FileContractID for
// testing
func fileContractID() types.FileContractID {
	var id types.FileContractID
	h := crypto.NewHash()
	h.Write(types.SpecifierFileContract[:])
	h.Sum(id[:0])
	return id
}
