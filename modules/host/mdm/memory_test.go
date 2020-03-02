package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/types"
)

// TestMemoryCost tests the value of MemoryCost for different situations to make
// sure we have sensible memory costs.
func TestMemoryCost(t *testing.T) {
	pt := newTestPriceTable()

	// Base program memory cost
	cost := MemoryCost(pt, InitMemory(), ProgramInitTime)
	expectedCost := types.NewCurrency64(10485760)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected append memory cost %v, got %v", expectedCost, cost)
	}

	// Append program
	cost = MemoryCost(pt, InitMemory()+AppendMemory(), ProgramInitTime+TimeAppend+TimeCommit)
	expectedCost = types.NewCurrency64(63170846720)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected append memory cost %v, got %v", expectedCost, cost)
	}
}
