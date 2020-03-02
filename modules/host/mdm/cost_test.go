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

	// Append
	//
	// TODO: This cost is based on SectorSize, which is small in tests. Should
	// we scale this so that we get an accurate picture of the actual cost?
	cost, refund := AppendCost(pt)
	expectedCost := types.NewCurrency64(8193)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected append cost %v, got %v", expectedCost, cost)
	}
	expectedRefund := types.NewCurrency64(4096)
	if !refund.Equals(expectedRefund) {
		t.Fatalf("expected append refund %v, got %v", expectedRefund, refund)
	}

	// Init
	//
	// TODO: Scale SectorSize?
	cost = InitCost(pt, modules.SectorSize)
	expectedCost = types.NewCurrency64(40961)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected init cost %v, got %v", expectedCost, cost)
	}

	// HasSector
	cost, refund = HasSectorCost(pt)
	expectedCost = types.NewCurrency64(1048576)
	if !cost.Equals(expectedCost) {
		t.Fatalf("expected hassector cost %v, got %v", expectedCost, cost)
	}
	expectedRefund = types.ZeroCurrency
	if !refund.Equals(expectedRefund) {
		t.Fatalf("expected hassector refund %v, got %v", expectedRefund, refund)
	}

	// TODO: Check the rest of the costs
}
