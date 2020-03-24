package main

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestFileHealthSummary is a regression test to check for panics
func TestFileHealthSummary(t *testing.T) {
	// Test with empty slice
	var dirs []directoryInfo
	renterFileHealthSummary(dirs)

	// Test with empty struct
	dirs = append(dirs, directoryInfo{})
	renterFileHealthSummary(dirs)
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
