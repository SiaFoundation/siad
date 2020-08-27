package modules

import (
	"gitlab.com/NebulousLabs/Sia/types"
)

// RenewBaseCosts is a helper to calculate the base costs for a renewed
// contract. The base costs consist of the initial storage cost and collateral
// associated with renewing a contract that already contains data.
func RenewBaseCosts(lastRev types.FileContractRevision, host HostExternalSettings, endHeight types.BlockHeight) (basePrice, baseCollateral types.Currency) {
	// Get the height until which the storage is already paid for, the height
	// until which we want to pay for storage and the amount of storage that
	// needs to be covered.
	paidForUntil := lastRev.NewWindowEnd
	payForUntil := endHeight + host.WindowSize
	storage := lastRev.NewFileSize
	// If the storage is already covered, or if there is no data yet, there is
	// no base cost associated with this renewal.
	if paidForUntil >= payForUntil || storage == 0 {
		return
	}
	// Otherwise we calculate the number of blocks we still need to pay for and
	// the amount of cost and collateral expected.
	timeExtension := uint64(payForUntil - paidForUntil)
	basePrice = host.StoragePrice.Mul64(storage).Mul64(timeExtension)    // cost of already uploaded data that needs to be covered by the renewed contract.
	baseCollateral = host.Collateral.Mul64(storage).Mul64(timeExtension) // same as basePrice.
	return
}
