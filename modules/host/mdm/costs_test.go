package mdm

import (
	"testing"
	"time"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// newTestWriteStorePriceTable returns a custom price table for the cost tests.
func newTestWriteStorePriceTable() *modules.RPCPriceTable {
	pt := &modules.RPCPriceTable{}
	pt.Validity = time.Minute

	pt.WriteBaseCost = types.ZeroCurrency
	pt.WriteLengthCost = types.ZeroCurrency
	pt.WriteStoreCost = modules.DefaultStoragePrice
	pt.DownloadBandwidthCost = types.ZeroCurrency
	pt.UploadBandwidthCost = types.ZeroCurrency
	return pt
}

// appendTrueCost returns the true, production cost of an append. This is
// necessary because in tests the sector size is only 4 KiB which leads to
// misleading costs.
func appendTrueCost(pt *modules.RPCPriceTable) (types.Currency, types.Currency) {
	writeCost := pt.WriteLengthCost.Mul64(modules.SectorSizeStandard).Add(pt.WriteBaseCost)
	storeCost := pt.WriteStoreCost.Mul64(modules.SectorSizeStandard) // potential refund
	return writeCost.Add(storeCost), storeCost
}

// TestCosts tests the costs for individual instructions so that we have a sense
// of their relative costs and to make sure they are sensible values.
func TestCosts(t *testing.T) {
	pt := newTestWriteStorePriceTable()

	// Define helper variables.
	sectorsPerTB := modules.BytesPerTerabyte.Div64(modules.SectorSizeStandard)

	// Append
	cost, refund := appendTrueCost(pt)
	// Scale the cost from a single, production-sized sector up to a TB of data.
	costPerTB := cost.Mul(sectorsPerTB)
	expectedCostPerTB := modules.DefaultStoragePrice.Mul(modules.BytesPerTerabyte)
	if !aboutEquals(costPerTB, expectedCostPerTB) {
		t.Errorf("expected append cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}
	// cost == refund because we are testing the storage costs, and the refund
	// comprises only the storage cost.
	expectedRefundPerTB := expectedCostPerTB
	refundPerTB := refund.Mul(sectorsPerTB)
	if !aboutEquals(refundPerTB, expectedRefundPerTB) {
		t.Errorf("expected append refund %v, got %v", expectedRefundPerTB.HumanString(), refundPerTB.HumanString())
	}
}

// TestAboutEquals verifies the correctness of the aboutEquals helper.
func TestAboutEquals(t *testing.T) {
	c := types.NewCurrency64
	tests := []struct {
		cExpected, cActual types.Currency
		out                bool
	}{
		{c(100), c(90), true},
		{c(100), c(110), true},
		{c(100), c(89), false},
		{c(100), c(111), false},
	}
	for _, test := range tests {
		out := aboutEquals(test.cExpected, test.cActual)
		if out != test.out {
			t.Errorf("aboutEquals(%v, %v): expected '%v', got '%v'", test.cExpected, test.cActual, test.out, out)
		}
	}
}

// aboutEquals checks that two currencies are approximately equal.
func aboutEquals(cExpected, cActual types.Currency) bool {
	// The precision with which we check results is 10% of the expected value. We
	// don't need to know that the exact cost of an append is
	// '25425636574074000000000000', we just need a rough value.
	errorWindow := cExpected.Div64(10)
	return cExpected.Add(errorWindow).Cmp(cActual) >= 0 && cExpected.Sub(errorWindow).Cmp(cActual) <= 0
}
