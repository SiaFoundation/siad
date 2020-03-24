package main

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/node/api"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestContractInfo is a regression test to check for negative currency panics
func TestContractInfo(t *testing.T) {
	contract := api.RenterContract{
		ID:        fileContractID(),
		Fees:      types.NewCurrency64(100),
		TotalCost: types.ZeroCurrency,
	}
	printContractInfo(contract.ID.String(), []api.RenterContract{contract})
}

func fileContractID() types.FileContractID {
	var id types.FileContractID
	h := crypto.NewHash()
	h.Write(types.SpecifierFileContract[:])
	h.Sum(id[:0])
	return id
}
