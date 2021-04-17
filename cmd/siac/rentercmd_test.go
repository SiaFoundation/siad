package main

import (
	"fmt"
	"testing"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/node/api"
	"go.sia.tech/siad/types"
)

// TestFilePercentageBreakdown tests the filePercentageBreakdown function
func TestFilePercentageBreakdown(t *testing.T) {
	// Test for Panics
	//
	// Test with empty slice
	var dirs []directoryInfo
	fileHealthBreakdown(dirs, false)
	// Test with empty struct
	dirs = append(dirs, directoryInfo{})
	fileHealthBreakdown(dirs, false)

	// Test basic directory info
	dir := modules.DirectoryInfo{AggregateNumFiles: 7}
	f1 := modules.FileInfo{MaxHealthPercent: 100}
	f2 := modules.FileInfo{MaxHealthPercent: 80}
	f3 := modules.FileInfo{MaxHealthPercent: 60, Stuck: true}
	f4 := modules.FileInfo{MaxHealthPercent: 30}
	f5 := modules.FileInfo{MaxHealthPercent: 10}
	f6 := modules.FileInfo{MaxHealthPercent: 0, OnDisk: true}
	f7 := modules.FileInfo{MaxHealthPercent: 0}
	files := []modules.FileInfo{f1, f2, f3, f4, f5, f6, f7}
	dirs[0] = directoryInfo{
		dir:   dir,
		files: files,
	}
	percentages, numStuck, err := fileHealthBreakdown(dirs, false)
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
	if numStuck != 1 {
		t.Errorf("Expected 1 stuck file but got %v", numStuck)
	}
	expected := float64(100) * float64(1) / float64(7)
	if err := checkValue(expected, percentages[0]); err != nil {
		t.Log("Full Health")
		t.Error(err)
	}
	if err := checkValue(expected, percentages[1]); err != nil {
		t.Log("Greater than 75")
		t.Error(err)
	}
	if err := checkValue(expected, percentages[2]); err != nil {
		t.Log("Greater than 50")
		t.Error(err)
	}
	if err := checkValue(expected, percentages[3]); err != nil {
		t.Log("Greater than 25")
		t.Error(err)
	}
	if err := checkValue(expected, percentages[5]); err != nil {
		t.Log("Unrecoverable")
		t.Error(err)
	}
	expected = float64(100) * float64(2) / float64(7)
	if err := checkValue(expected, percentages[4]); err != nil {
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
