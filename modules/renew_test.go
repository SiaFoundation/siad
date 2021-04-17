package modules

import (
	"testing"

	"go.sia.tech/siad/types"
)

// TestRenewBaseCost is a unit test for RenewBaseCosts.
func TestRenewBaseCost(t *testing.T) {
	var pt RPCPriceTable
	pt.WriteStoreCost = types.SiacoinPrecision
	pt.CollateralCost = types.SiacoinPrecision.Mul64(2)
	pt.RenewContractCost = types.SiacoinPrecision
	pt.WindowSize = 50

	// Declare test cases.
	tests := []struct {
		oldWindowEnd types.BlockHeight
		newEndHeight types.BlockHeight
		storage      uint64

		basePrice      types.Currency
		baseCollateral types.Currency
	}{
		// No storage.
		{
			oldWindowEnd: 0,
			newEndHeight: 10,
			storage:      0,

			basePrice:      types.SiacoinPrecision,
			baseCollateral: types.ZeroCurrency,
		},
		// 1 block time extension
		{
			oldWindowEnd: 49,
			newEndHeight: 0,
			storage:      1,

			basePrice:      types.SiacoinPrecision.Mul64(2),
			baseCollateral: types.SiacoinPrecision.Mul64(2),
		},
		// 0 block time extension.
		{
			oldWindowEnd: 50,
			newEndHeight: 0,
			storage:      1,

			basePrice:      types.SiacoinPrecision,
			baseCollateral: types.ZeroCurrency,
		},
		// -1 block time extension.
		{
			oldWindowEnd: 51,
			newEndHeight: 0,
			storage:      1,

			basePrice:      types.SiacoinPrecision,
			baseCollateral: types.ZeroCurrency,
		},
		// 60 block time extension
		{
			oldWindowEnd: 0,
			newEndHeight: 10,
			storage:      1,

			basePrice:      types.SiacoinPrecision.Mul64(61),
			baseCollateral: types.SiacoinPrecision.Mul64(120),
		},
	}

	// Run tests.
	for i, test := range tests {
		var lastRev types.FileContractRevision
		lastRev.NewWindowEnd = test.oldWindowEnd
		lastRev.NewFileSize = test.storage
		endHeight := test.newEndHeight
		basePrice, baseCollateral := RenewBaseCosts(lastRev, &pt, endHeight)

		if !basePrice.Equals(test.basePrice) {
			t.Fatalf("%v: expected basePrice %v but was %v", i, test.basePrice, basePrice)
		}
		if !baseCollateral.Equals(test.baseCollateral) {
			t.Fatalf("%v: expected baseCollateral %v but was %v", i, test.baseCollateral, baseCollateral)
		}
	}
}
