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

	// Define helper variables.
	sc := types.SiacoinPrecision
	perTB := modules.BytesPerTerabyte

	// Set the precision with which to check results to 0.1 SC. We don't need to
	// know that the exact cost of an append is '25425636574074000000000000', we
	// just need a rough value.
	precision := sc.Div64(10)

	// Init for a TB of data
	tb, err := perTB.Uint64()
	if err != nil {
		t.Error(err)
	}
	cost := modules.MDMInitCost(pt, tb)
	expectedCost := sc.Div64(1e3).Mul64(38).Div64(10) // 3.8 mS
	if !aboutEquals(cost, expectedCost, precision.Div64(1e3)) {
		t.Errorf("expected init cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}

	// Append
	cost, refund := modules.MDMAppendCost(pt)
	costPerTB := cost.Div64(modules.SectorSize).Mul(perTB)
	expectedCostPerTB := sc.Mul64(254).Div64(10) // 25.4 SC
	if !aboutEquals(costPerTB, expectedCostPerTB, precision) {
		t.Errorf("expected append cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}
	expectedRefundPerTB := sc.Div64(1e3).Mul64(115).Div64(10) // 11.5 mS
	refundPerTB := refund.Div64(modules.SectorSize).Mul(perTB)
	if !aboutEquals(refundPerTB, expectedRefundPerTB, precision.Div64(1e3)) {
		t.Errorf("expected append refund %v, got %v", expectedRefundPerTB.HumanString(), refundPerTB.HumanString())
	}

	// DropSectors
	cost, refund = modules.MDMDropSectorsCost(pt, 1)
	expectedCost = sc.Div64(1e6).Mul64(21).Div64(10) // 2.1uS
	if !aboutEquals(cost, expectedCost, precision.Div64(1e6)) {
		t.Errorf("expected dropsectors cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}
	expectedRefund := types.ZeroCurrency
	if !aboutEquals(refund, expectedRefund, precision) {
		t.Errorf("expected dropsectors refund %v, got %v", expectedRefund.HumanString(), refund.HumanString())
	}

	// HasSector
	cost, refund = modules.MDMHasSectorCost(pt)
	expectedCost = sc.Div64(1e12).Mul64(4045).Div64(10) // 404.5 pS
	if !aboutEquals(cost, expectedCost, precision.Div64(1e12)) {
		t.Errorf("expected hassector cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}
	expectedRefund = types.ZeroCurrency
	if !refund.Equals(expectedRefund) {
		t.Errorf("expected hassector refund %v, got %v", expectedRefund, refund)
	}

	// Read
	costPerTB, refundPerTB = modules.MDMReadCost(pt, 1e12)
	expectedCostPerTB = sc.Mul64(25) // 25 SC
	if !aboutEquals(costPerTB, expectedCostPerTB, precision) {
		t.Errorf("expected read cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}
	expectedRefundPerTB = types.ZeroCurrency
	if !refundPerTB.Equals(expectedRefundPerTB) {
		t.Errorf("expected read refund %v, got %v", expectedRefundPerTB, refundPerTB)
	}
}

// TestAboutEquals verifies the correctness of the aboutEquals helper.
func TestAboutEquals(t *testing.T) {
	c := types.NewCurrency64
	tests := []struct {
		c1, c2, precision types.Currency
		out               bool
	}{
		{c(105), c(100), c(5), true},
		{c(100), c(105), c(5), true},
		{c(100), c(105), c(4), false},
		{c(105), c(100), c(4), false},
	}
	for _, test := range tests {
		out := aboutEquals(test.c1, test.c2, test.precision)
		if out != test.out {
			t.Errorf("aboutEquals(%v, %v, %v): expected '%v', got '%v'", test.c1, test.c2, test.precision, test.out, out)
		}
	}
}

// aboutEquals checks that two currencies are approximately equal, within the given error window.
func aboutEquals(c1, c2, errorWindow types.Currency) bool {
	return c1.Cmp(c2.Add(errorWindow)) <= 0 && c2.Cmp(c1.Add(errorWindow)) <= 0
}
