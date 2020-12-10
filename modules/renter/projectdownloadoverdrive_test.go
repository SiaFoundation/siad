package renter

import (
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
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

// TestProjectDownloadChunk_adjustedReadDuration is a unit test for the
// 'adjustedReadDuration' function on the pdc.
func TestProjectDownloadChunk_adjustedReadDuration(t *testing.T) {
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

// TestProjectDownloadChunk_bestOverdriveUnresolvedWorker is a unit test for the
// 'bestOverdriveUnresolvedWorker' function on the pdc
func TestProjectDownloadChunk_bestOverdriveUnresolvedWorker(t *testing.T) {
	t.Parallel()

	now := time.Now()
	max := time.Duration(math.MaxInt64)

	// mockWorker is a helper function that returns a worker with a pricetable
	// and an initialised read queue that returns a non zero value for read
	// estimates
	mockWorker := func(jobsCompleted float64) *worker {
		worker := new(worker)
		worker.newPriceTable()
		worker.staticPriceTable().staticPriceTable = newDefaultPriceTable()
		worker.initJobReadQueue()
		worker.staticJobReadQueue.weightedJobTime64k = float64(time.Second)
		worker.staticJobReadQueue.weightedJobsCompleted64k = jobsCompleted
		return worker
	}

	// mock a pdc
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16
	pdc.pricePerMS = types.SiacoinPrecision.Div64(1e6)

	// verify return params for an empty array of unresolved workers
	uws := []*pcwsUnresolvedWorker{}
	exists, late, dur, waitDur, wIndex := pdc.bestOverdriveUnresolvedWorker(uws)
	if exists || !late || dur != max || waitDur != max || wIndex != -1 {
		t.Fatal("unexpected")
	}

	// mock two workers with different traits
	w1 := mockWorker(10) // avg 100ms job time
	w2 := mockWorker(5)  // avg 200ms job time
	uws = []*pcwsUnresolvedWorker{
		{
			staticWorker:               w1,
			staticExpectedCompleteTime: now.Add(200 * time.Millisecond),
		}, // ~300ms total dur
		{
			staticWorker:               w2,
			staticExpectedCompleteTime: now.Add(50 * time.Millisecond),
		}, // ~250ms total dur
	}

	// verify the best overdrive worker has the expected outcome values for w2
	exists, late, dur, waitDur, wIndex = pdc.bestOverdriveUnresolvedWorker(uws)
	if !exists || late || wIndex != 1 {
		t.Fatal("unexpected")
	}

	// now make w2 very expensive, the best overdrive worker should become w1
	w2.staticPriceTable().staticPriceTable.ReadBaseCost = types.SiacoinPrecision
	exists, late, dur, waitDur, wIndex = pdc.bestOverdriveUnresolvedWorker(uws)
	if !exists || late || wIndex != 0 {
		t.Fatal("unexpected")
	}

	// now alter w1 to be late, the best overdrive worker should become w2
	uws[0].staticExpectedCompleteTime = now.Add(-50 * time.Millisecond)
	exists, late, dur, waitDur, wIndex = pdc.bestOverdriveUnresolvedWorker(uws)
	if !exists || late || wIndex != 1 {
		t.Fatal("unexpected")
	}

	// now alter w2 to be late as well, we expect the worker with the lowest
	// read time to be the best one here
	uws[1].staticExpectedCompleteTime = now.Add(-100 * time.Millisecond)
	exists, late, dur, waitDur, wIndex = pdc.bestOverdriveUnresolvedWorker(uws)
	if !exists || !late || waitDur != max || wIndex != 0 {
		t.Fatal("unexpected")
	}
}

// TestProjectDownloadChunk_overdriveStatus is a unit test for the
// 'overdriveStatus' function on the pdc.
func TestProjectDownloadChunk_overdriveStatus(t *testing.T) {
	t.Parallel()

	now := time.Now()

	pcws := new(projectChunkWorkerSet)
	pcws.staticErasureCoder = modules.NewRSCodeDefault()

	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.availablePieces = [][]*pieceDownload{
		{
			{expectedCompleteTime: now.Add(-1 * time.Minute)},
			{expectedCompleteTime: now.Add(-3 * time.Minute)},
		},
		{
			{expectedCompleteTime: now.Add(-2 * time.Minute)},
		},
	}

	// verify we return the correct amount of overdrive workers that need to be
	// launched if no pieces have launched yet, also verify last return time
	toLaunch, returnTime := pdc.overdriveStatus()
	if toLaunch != modules.RenterDefaultDataPieces {
		t.Fatal("unexpected")
	}
	if returnTime != (time.Time{}) {
		t.Fatal("unexpected", returnTime)
	}

	// launch a piece and verify we get 1 worker to launch due to the return
	// time being in the past
	pdc.availablePieces[0][0].launched = true
	toLaunch, returnTime = pdc.overdriveStatus()
	if toLaunch != 1 {
		t.Fatal("unexpected")
	}
	if returnTime != now.Add(-1*time.Minute) {
		t.Fatal("unexpected")
	}

	// add a piecedownload that returns somewhere in the future
	pdc.availablePieces[1] = append(pdc.availablePieces[1], &pieceDownload{
		launched:             true,
		expectedCompleteTime: now.Add(time.Minute),
	})
	toLaunch, returnTime = pdc.overdriveStatus()
	if toLaunch != 0 {
		t.Fatal("unexpected")
	}
	if returnTime != now.Add(time.Minute) {
		t.Fatal("unexpected")
	}
}
