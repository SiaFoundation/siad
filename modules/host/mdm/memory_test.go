package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestMemoryCost tests the value of MemoryCost for different instructions to
// make sure we have sensible memory costs.
func TestMemoryCost(t *testing.T) {
	pt := newTestPriceTable()

	// Define helper variables.
	sc := types.SiacoinPrecision
	perTB := modules.BytesPerTerabyte

	// Base program memory cost
	cost := modules.MDMMemoryCost(pt, modules.MDMInitMemory(), modules.MDMTimeInitProgramBase)
	expectedCost := sc.Div64(1e9).Mul64(28).Div64(10) // 2.8 nS
	if !aboutEquals(cost, expectedCost) {
		t.Errorf("expected base memory cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}

	// Append
	appendMemory := modules.SectorSizeStandard // override MDMAppendMemory()
	cost = modules.MDMMemoryCost(pt, appendMemory, modules.MDMTimeAppend)
	costPerTB := cost.Div64(appendMemory).Mul(perTB)
	expectedCostPerTB := sc.Div64(1e3).Mul64(27).Div64(10) // 2.7 mS
	if !aboutEquals(costPerTB, expectedCostPerTB) {
		t.Errorf("expected append memory cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}

	// Two Appends. The memory cost should grow more than linearly.
	appendMemory *= 2
	cost = cost.Add(modules.MDMMemoryCost(pt, appendMemory, modules.MDMTimeAppend))
	costPerTB = cost.Div64(appendMemory).Mul(perTB)
	expectedCostPerTB = sc.Div64(1e3).Mul64(40).Div64(10) // 4.0 mS
	if !aboutEquals(costPerTB, expectedCostPerTB) {
		t.Errorf("expected double append memory cost %v, got %v", expectedCostPerTB.HumanString(), costPerTB.HumanString())
	}

	// DropSectors
	cost = modules.MDMMemoryCost(pt, modules.MDMDropSectorsMemory(), modules.MDMTimeDropSectorsBase + modules.MDMTimeDropSingleSector)
	expectedCost = types.ZeroCurrency
	if cost.Cmp(expectedCost) != 0 {
		t.Errorf("expected dropsectors memory cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}

	// HasSector
	cost = modules.MDMMemoryCost(pt, modules.MDMHasSectorMemory(), modules.MDMTimeHasSector)
	expectedCost = types.ZeroCurrency
	if cost.Cmp(expectedCost) != 0 {
		t.Errorf("expected hassector memory cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}

	// Read
	cost = modules.MDMMemoryCost(pt, modules.MDMReadMemory(), modules.MDMTimeReadSector)
	expectedCost = types.ZeroCurrency
	if cost.Cmp(expectedCost) != 0 {
		t.Errorf("expected read memory cost %v, got %v", expectedCost.HumanString(), cost.HumanString())
	}
}
