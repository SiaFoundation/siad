package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCosts tests the costs for individual instructions so that we have a sense
// of their relative costs and to make sure they are sensible values.
func TestCosts(t *testing.T) {
	pt := newTestPriceTable()

	// Set the precision with which to check results to 0.1 SC. We don't need to
	// know that the exact cost of an append is '25425636574074000000000000', we
	// just need a rough value.
	equalityPrecision := types.SiacoinPrecision.Div64(10)

	// Append
	cost, refund := modules.MDMAppendCost(pt)
	costPerTB := cost.Div64(modules.SectorSize).Mul(modules.BytesPerTerabyte)
	expectedCostPerTB := types.SiacoinPrecision.Mul64(254).Div64(10) // 25.4 SC
	if !aboutEquals(costPerTB, expectedCostPerTB, equalityPrecision) {
		t.Fatalf("expected append cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}
	expectedRefundPerTB := types.SiacoinPrecision.Div64(1 << 10).Mul64(115).Div64(10) // 11.5 mS
	refundPerTB := refund.Div64(modules.SectorSize).Mul(modules.BytesPerTerabyte)
	if !aboutEquals(refundPerTB, expectedRefundPerTB, equalityPrecision.Div64(1<<3)) {
		t.Fatalf("expected append refund %v, got %v", expectedRefundPerTB.HumanString(), refundPerTB.HumanString())
	}

	// Init for a TB of data
	// TODO
	cost = modules.MDMInitCost(pt, modules.SectorSize)
	expectedCost := types.NewCurrency64(40961)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected init cost %v, got %v", expectedCost, cost)
	}

	// HasSector
	// TODO
	cost, refund = modules.MDMHasSectorCost(pt)
	expectedCost = types.NewCurrency64(1048576)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected hassector cost %v, got %v", expectedCost, cost)
	}
	expectedRefund := types.ZeroCurrency
	if !refund.Equals(expectedRefund) {
		t.Fatalf("expected hassector refund %v, got %v", expectedRefund, refund)
	}

	// TODO: Check the rest of the costs
}

// aboutEquals checks that two currencies are approximately equal, within the given error window.
func aboutEquals(c1, c2, errorWindow types.Currency) bool {
	return c1.Cmp(c2.Add(errorWindow)) < 0 && c2.Cmp(c1.Add(errorWindow)) < 0
}
