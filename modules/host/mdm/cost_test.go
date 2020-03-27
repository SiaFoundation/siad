package mdm

import (
	"math"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestCostForAppendProgram calculates the cost for a program which appends 1
// TiB of data.
//
// NOTE: We use a modified cost function for Append which returns the cost for
// production-sized sectors, as sectors in testing are only 4 KiB.
func TestCostForAppendProgram(t *testing.T) {
	pt := newTestPriceTable()

	// Define helper variables.
	sc := types.SiacoinPrecision

	// Initialize starting values.
	numInstructions := uint64(math.Ceil(1e12 / float64(modules.SectorSizeStandard)))
	runningCost := modules.MDMInitCost(pt, 1e12, numInstructions)
	runningRefund := types.ZeroCurrency
	runningMemory := modules.MDMInitMemory()
	runningSize := uint64(0)

	// Simulate running a program to append 1 TiB of data.
	for runningSize < (1e12) {
		cost, refund := appendTrueCost(pt)
		memory := modules.SectorSizeStandard // override MDMAppendMemory()
		time := uint64(modules.MDMTimeAppend)
		runningCost, runningRefund, runningMemory = updateRunningCosts(pt, runningCost, runningRefund, runningMemory, cost, refund, memory, time)
		runningSize += modules.SectorSizeStandard
	}
	runningCost = runningCost.Add(modules.MDMMemoryCost(pt, runningMemory, modules.MDMTimeCommit))

	expectedCost := sc.Mul64(321) // 321.1 SC
	if !aboutEquals(expectedCost, runningCost) {
		t.Errorf("expected cost for appending 1 TiB to be %v, got cost %v", expectedCost.HumanString(), runningCost.HumanString())
	}

	expectedRefund := sc.Div64(1000).Mul64(116).Div64(10) // 11.6 mS
	if !aboutEquals(expectedRefund, runningRefund) {
		t.Errorf("expected refund for appending 1 TiB to be %v, got refund %v", expectedRefund.HumanString(), runningRefund.HumanString())
	}

	expectedMemory := runningSize + modules.MDMInitMemory() // 1 TiB + 1 MiB
	if expectedMemory != runningMemory {
		t.Errorf("expected memory for appending 1 TiB to be %v, got %v", expectedMemory, runningMemory)
	}
}

// appendTrueCost returns the true, production cost of an append. This is
// necessary because in tests the sector size is only 4 KiB and the append cost
// is misleading.
func appendTrueCost(pt modules.RPCPriceTable) (types.Currency, types.Currency) {
	writeCost := pt.WriteLengthCost.Mul64(modules.SectorSizeStandard).Add(pt.WriteBaseCost)
	storeCost := pt.WriteStoreCost.Mul64(modules.SectorSizeStandard) // potential refund
	return writeCost.Add(storeCost), storeCost
}

// TestCosts tests the costs for individual instructions so that we have a sense
// of their relative costs and to make sure they are sensible values.
func TestCosts(t *testing.T) {
	pt := newTestPriceTable()

	// Define helper variables.
	sc := types.SiacoinPrecision
	perTB := modules.BytesPerTerabyte

	// Init for a TB of data
	cost := modules.MDMInitCost(pt, 1e12, 0)
	expectedCost := sc.Div64(1e3).Mul64(27).Div64(10) // 2.7 mS
	if !aboutEquals(cost, expectedCost) {
		t.Errorf("expected init cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}

	// Append
	cost, refund := appendTrueCost(pt)
	// Scale the costs up to a TB of data.
	costPerTB := cost.Div64(modules.SectorSizeStandard).Mul(perTB)
	expectedCostPerTB := sc // 1 SC
	if !aboutEquals(costPerTB, expectedCostPerTB) {
		t.Errorf("expected append cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}
	expectedRefundPerTB := sc.Div64(1e3).Mul64(115).Div64(10) // 11.5 mS
	refundPerTB := refund.Div64(modules.SectorSizeStandard).Mul(perTB)
	if !aboutEquals(refundPerTB, expectedRefundPerTB) {
		t.Errorf("expected append refund %v, got %v", expectedRefundPerTB.HumanString(), refundPerTB.HumanString())
	}

	// DropSectors
	cost, refund = modules.MDMDropSectorsCost(pt, 1)
	expectedCost = sc.Div64(1e6).Mul64(21).Div64(10) // 2.1 uS
	if !aboutEquals(cost, expectedCost) {
		t.Errorf("expected dropsectors cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}
	expectedRefund := types.ZeroCurrency
	if !aboutEquals(refund, expectedRefund) {
		t.Errorf("expected dropsectors refund %v, got %v", expectedRefund.HumanString(), refund.HumanString())
	}

	// HasSector
	cost, refund = modules.MDMHasSectorCost(pt)
	expectedCost = sc.Div64(1e12).Mul64(28).Div64(10) // 2.8 pS
	if !aboutEquals(cost, expectedCost) {
		t.Errorf("expected hassector cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}
	expectedRefund = types.ZeroCurrency
	if !refund.Equals(expectedRefund) {
		t.Errorf("expected hassector refund %v, got %v", expectedRefund, refund)
	}

	// Read
	costPerTB, refundPerTB = modules.MDMReadCost(pt, 1e12)
	expectedCostPerTB = sc.Mul64(25) // 25 SC
	if !aboutEquals(costPerTB, expectedCostPerTB) {
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
