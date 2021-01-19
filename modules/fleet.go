package modules

// fleet.go contains data structures which are commonly used across programs for
// managing a fleet of siad nodes.

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// RenterStats is a struct which tracks key metrics in a single renter.
type RenterStats struct {
	// A name for this renter.
	Name string

	// The total amount of contract data that hosts are maintaining on behalf of
	// the renter is the sum of these fields.
	ActiveContractData uint64
	PassiveContractData uint64
	WastedContractData uint64

	TotalSiafiles uint64

	TotalContractSpentFunds types.Currency // Includes fees
	TotalContractFeeSpending types.Currency
	TotalContractRemainingFunds types.Currency

	TotalWalletFunds types.Currency // Includes unconfirmed
}

// RenterFleetStats contains aggregated metrics across a fleet of renter nodes.
// Where the fields match, they are just a sum of that field across all of the
// RawStats.
type RenterFleetStats struct {
	// RawStats contains the RenterStats for each renter in the fleet.
	RawStats []RenterStats

	ActiveContractData uint64
	PassiveContractData uint64
	WastedContractData uint64

	TotalSiafiles uint64

	TotalContractSpentFunds types.Currency
	TotalContractFeeSpending types.Currency
	TotalContractRemainingFunds types.Currency

	TotalWalletFunds types.Currency
}
