package renter

import (
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestAddCostPenalty is a unit test that covers the `addCostPenalty` helper
// function.
func TestAddCostPenalty(t *testing.T) {
	// verify overflow
	jt := time.Duration(1)
	jc := types.NewCurrency64(math.MaxUint64).Mul64(10)
	pricePerMS := types.NewCurrency64(2)
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 on overflow")
	}

	// verify penalty higher than MaxInt64
	jt = time.Duration(1)
	jc = types.NewCurrency64(math.MaxInt64).Add64(1)
	pricePerMS = types.NewCurrency64(1)
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 when penalty exceeds MaxInt64")
	}

	// verify high job time overflowing after adding penalty
	jc = types.NewCurrency64(10)
	pricePerMS = types.NewCurrency64(1)   // penalty is 10
	jt = time.Duration(math.MaxInt64 - 5) // job time + penalty exceeds MaxInt64
	jt = addCostPenalty(jt, jc, pricePerMS)
	if jt != time.Duration(math.MaxInt64) {
		t.Error("Expected job time to be adjusted to MaxInt64 when job time + penalty exceeds MaxInt64")
	}

	// verify happy case
	jt = time.Duration(fastrand.Intn(10) + 1)
	jc = types.NewCurrency64(fastrand.Uint64n(100) + 1)
	pricePerMS = types.NewCurrency64(fastrand.Uint64n(10) + 1)
	adjusted := addCostPenalty(jt, jc, pricePerMS)
	if adjusted <= jt {
		t.Error("unexpected")
	}

	// verify we assert pricePerMS to be higher than zero
	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected a panic when pricePerMS is zero")
		}
	}()
	addCostPenalty(jt, jc, types.ZeroCurrency)
}
