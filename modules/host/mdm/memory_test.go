package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestMemoryCost tests the value of MemoryCost for different situations to make
// sure we have sensible memory costs.
func TestMemoryCost(t *testing.T) {
	pt := newTestPriceTable()

	// Base program memory cost
	cost := modules.MDMMemoryCost(pt, modules.MDMInitMemory(), modules.MDMProgramInitTime)
	expectedCost := types.NewCurrency64(10485760)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected append memory cost %v, got %v", expectedCost, cost)
	}

	// Append program
	cost = modules.MDMMemoryCost(pt, modules.MDMInitMemory()+modules.MDMAppendMemory(), modules.MDMProgramInitTime+modules.MDMTimeAppend+modules.MDMTimeCommit)
	expectedCost = types.NewCurrency64(63170846720)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected append memory cost %v, got %v", expectedCost, cost)
	}
}
