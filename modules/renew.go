package modules

import (
	"go.sia.tech/siad/types"
)

// RenewBaseCosts is a helper to calculate the base costs for a renewed
// contract. The base costs consist of the initial storage cost and collateral
// associated with renewing a contract that already contains data.
// NOTE: The baseCollateral is computed using the maximum acceptable collateral
// to the host. Because of siafund fees, a renter may decide to use less than
// the amount of collateral advertised by the host. If the renter would rather
// have lower collateral and pay fewer siafund fees, they have the full freedom
// within the protocol to do that. It is strictly advantageous for the host.
func RenewBaseCosts(lastRev types.FileContractRevision, pt *RPCPriceTable, endHeight types.BlockHeight) (basePrice, baseCollateral types.Currency) {
	// Get the height until which the storage is already paid for, the height
	// until which we want to pay for storage and the amount of storage that
	// needs to be covered.
	paidForUntil := lastRev.NewWindowEnd
	payForUntil := endHeight + pt.WindowSize
	storage := lastRev.NewFileSize
	// The base is the rpc cost.
	basePrice = pt.RenewContractCost
	// If the storage is already covered, or if there is no data yet, there is
	// no base cost associated with this renewal.
	if paidForUntil >= payForUntil || storage == 0 {
		return
	}
	// Otherwise we calculate the number of blocks we still need to pay for and
	// the amount of cost and collateral expected.
	timeExtension := uint64(payForUntil - paidForUntil)
	basePrice = basePrice.Add(pt.WriteStoreCost.Mul64(storage).Mul64(timeExtension)) // cost of already uploaded data that needs to be covered by the renewed contract.
	baseCollateral = pt.CollateralCost.Mul64(storage).Mul64(timeExtension)           // same as basePrice.
	return
}
