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

// TestProjectDownloadChunkAdjustedReadDuration is a unit test for the
// 'adjustedReadDuration' function on the pdc.
func TestProjectDownloadChunkAdjustedReadDuration(t *testing.T) {
	t.Parallel()

	// mock a worker, ensure the readqueue returns a non zero time estimate
	worker := new(worker)
	worker.newPriceTable()
	worker.staticPriceTable().staticPriceTable = newDefaultPriceTable()
	worker.initJobReadQueue()
	worker.staticJobReadQueue.weightedJobTime64k = float64(time.Second)
	worker.staticJobReadQueue.weightedJobsCompleted64k = 10
	jrq := worker.staticJobReadQueue

	// fetch the expected job time for a 64kb download job, verify it's not 0
	jobTime := jrq.callExpectedJobTime(1 << 16)
	if jobTime == time.Duration(0) {
		t.Fatal("unexpected")
	}

	// mock a pdc with a 64kb piece length
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16
	pdc.pricePerMS = types.SiacoinPrecision

	// verify the adjusted read duration adds a cost penalty
	duration := pdc.adjustedReadDuration(worker)
	if duration <= jobTime {
		t.Fatal("unexpected", duration, jobTime)
	}
}
