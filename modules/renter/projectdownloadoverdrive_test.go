package renter

import (
	"math"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestAddCostPenalty is a unit test that covers the `addCostPenalty` helper
// function.
func TestAddCostPenalty(t *testing.T) {
	t.Parallel()

	// verify happy case
	jt := time.Duration(fastrand.Intn(10) + 1)
	jc := types.NewCurrency64(fastrand.Uint64n(100) + 10)
	pricePerMS := types.NewCurrency64(5)

	// calculate the expected outcome
	penalty, err := jc.Div(pricePerMS).Uint64()
	if err != nil {
		t.Fatal(err)
	}
	expected := jt + (time.Duration(penalty) * time.Millisecond)
	adjusted := addCostPenalty(jt, jc, pricePerMS)
	if adjusted != expected {
		t.Error("unexpected", adjusted, expected)
	}

	// verify no penalty if pricePerMS is higher than the cost of the job
	adjusted = addCostPenalty(jt, jc, types.SiacoinPrecision)
	if adjusted != jt {
		t.Error("unexpected")
	}

	// verify overflow
	jt = time.Duration(1)
	jc = types.NewCurrency64(math.MaxUint64).Mul64(10)
	pricePerMS = types.NewCurrency64(2)
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
}

// TestExpBackoffDelayMS is a unit test that probes the expBackoffDelayMS helper
// function
func TestExpBackoffDelayMS(t *testing.T) {
	t.Parallel()

	maxDelay := time.Duration(maxExpBackoffDelayMS) * time.Millisecond
	for i := 0; i < 20; i++ {
		if expBackoffDelayMS(i) == time.Duration(0) {
			t.Fatal("unexpected", i) // verify not null
		}
		if expBackoffDelayMS(i) > maxDelay {
			t.Fatal("unexpected") // verify max delay
		}
		if i > maxExpBackoffRetryCount && expBackoffDelayMS(i) != maxDelay {
			t.Fatal("unexpected") // verify max delay for retry count over 15
		}
	}
}

// TestProjectDownloadChunk_adjustedReadDuration is a unit test for the
// 'adjustedReadDuration' function on the pdc.
func TestProjectDownloadChunk_adjustedReadDuration(t *testing.T) {
	t.Parallel()

	// mock a worker, ensure the readqueue returns a non zero time estimate
	worker := mockWorker(100 * time.Millisecond)
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

	// verify the duration is not adjusted, due to the very high pricePerMS
	duration := pdc.adjustedReadDuration(worker)
	if duration != jobTime {
		t.Fatal("unexpected", duration, jobTime)
	}

	// set the pricePerMS to a sane value, that is lower than the job cost,
	// expected the duration to be adjusted
	pdc.pricePerMS = types.SiacoinPrecision.MulFloat(1e-12)
	duration = pdc.adjustedReadDuration(worker)
	if duration <= jobTime {
		t.Fatal("unexpected", duration, jobTime)
	}

	// put the read queue on a cooldown, verify it is reflected in the duration
	jrq.cooldownUntil = time.Now().Add(time.Minute)
	prevDur := duration
	duration = pdc.adjustedReadDuration(worker)
	if duration <= prevDur {
		t.Fatal("unexpected", duration, prevDur)
	}
}

// TestProjectDownloadChunk_findBestOverdriveWorker is a unit test for the
// 'findBestOverdriveWorker' function on the pdc
func TestProjectDownloadChunk_findBestOverdriveWorker(t *testing.T) {
	t.Parallel()

	now := time.Now()
	ec := modules.NewRSSubCodeDefault()

	// mock two workers with different traits
	w1 := mockWorker(200 * time.Millisecond) // avg 200ms job time
	w1.staticHostPubKeyStr = "w1"
	w2 := mockWorker(100 * time.Millisecond) // avg 100ms job time
	w2.staticHostPubKeyStr = "w2"

	// mock a pdc
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16
	pdc.pricePerMS = types.SiacoinPrecision.MulFloat(1e-12) // pS
	pdc.workerState = &pcwsWorkerState{
		unresolvedWorkers: map[string]*pcwsUnresolvedWorker{
			"w1": {
				staticWorker:               w1,
				staticExpectedResolvedTime: now.Add(50 * time.Millisecond),
			}, // ~250ms total dur
			"w2": {
				staticWorker:               w2,
				staticExpectedResolvedTime: now.Add(10 * time.Millisecond),
			}, // ~110ms total dur
		},
	}

	// basic case with no available cases should match the 1st case of
	// 'TestProjectDownloadChunk_bestOverdriveUnresolvedWorker'
	pdc.availablePieces = make([][]*pieceDownload, ec.NumPieces())

	// verify the pdc currently has no good overdrive worker yet, as there are
	// no available pieces and thus no available workers
	worker, _, _, _ := pdc.findBestOverdriveWorker()
	if worker != nil {
		t.Fatal("unexpected", worker)
	}

	// add an available piece for worker 1 for the 2nd piece
	pdc.availablePieces = make([][]*pieceDownload, ec.NumPieces())
	delete(pdc.workerState.unresolvedWorkers, "w1")
	pdc.availablePieces[2] = append(pdc.availablePieces[2], &pieceDownload{
		worker: w1,
	})

	// expect the worker to still be nil, because we have an unresolved worker
	// that has a better estimate
	worker, _, _, _ = pdc.findBestOverdriveWorker()
	if worker != nil {
		t.Fatal("unexpected", worker)
	}

	// tweak it so the unresolved becomes slower than the first worker, for
	// which we have an available piece
	pdc.workerState.unresolvedWorkers["w2"].staticExpectedResolvedTime = now.Add(200 * time.Millisecond)
	worker, pieceIndex, _, _ := pdc.findBestOverdriveWorker()
	if worker != w1 {
		t.Fatal("unexpected", worker)
	}
	if pieceIndex != 2 {
		t.Fatal("unexpected", pieceIndex)
	}

	// now do the same for worker 2, because w2 is faster it should now become
	// the best available worker
	delete(pdc.workerState.unresolvedWorkers, "w2")
	pdc.availablePieces[1] = append(pdc.availablePieces[1], &pieceDownload{
		worker: w2,
	})
	worker, pieceIndex, _, _ = pdc.findBestOverdriveWorker()
	if worker != w2 {
		t.Fatal("unexpected", worker.staticHostPubKeyStr)
	}
	if pieceIndex != 1 {
		t.Fatal("unexpected", pieceIndex)
	}

	// now mock a cooldown on w2's jobread queue, it should now favor w1
	w2.staticJobReadQueue.cooldownUntil = time.Now().Add(time.Minute)
	worker, pieceIndex, _, _ = pdc.findBestOverdriveWorker()
	if worker != w1 {
		t.Fatal("unexpected", worker.staticHostPubKeyStr)
	}
	if pieceIndex != 2 {
		t.Fatal("unexpected", pieceIndex)
	}

	// mock worker 1 completed downloading piece at index 2, seeing as piece at
	// index 2 is complete, it should get skipped and worker 2 should now become
	// the most interesting overdrive working
	pdc.availablePieces[2][0].completed = true

	worker, pieceIndex, _, _ = pdc.findBestOverdriveWorker()
	if worker != w2 {
		t.Fatal("unexpected", worker.staticHostPubKeyStr)
	}
	if pieceIndex != 1 {
		t.Fatal("unexpected", pieceIndex)
	}
}

// TestProjectDownloadChunk_bestOverdriveUnresolvedWorker is a unit test for the
// 'bestOverdriveUnresolvedWorker' function on the pdc
func TestProjectDownloadChunk_bestOverdriveUnresolvedWorker(t *testing.T) {
	t.Parallel()

	now := time.Now()
	max := time.Duration(math.MaxInt64)

	// mock a pdc
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16
	pdc.pricePerMS = types.SiacoinPrecision.MulFloat(1e-12) // pS

	// verify return params for an empty array of unresolved workers
	uws := []*pcwsUnresolvedWorker{}
	exists, late, dur, waitDur, wIndex := pdc.bestOverdriveUnresolvedWorker(uws)
	if exists || !late || dur != max || waitDur != max || wIndex != -1 {
		t.Fatal("unexpected")
	}

	// mock two workers with different traits
	w1 := mockWorker(100 * time.Millisecond) // avg 100ms job time
	w2 := mockWorker(200 * time.Millisecond) // avg 200ms job time
	uws = []*pcwsUnresolvedWorker{
		{
			staticWorker:               w1,
			staticExpectedResolvedTime: now.Add(200 * time.Millisecond),
		}, // ~300ms total dur
		{
			staticWorker:               w2,
			staticExpectedResolvedTime: now.Add(50 * time.Millisecond),
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
	uws[0].staticExpectedResolvedTime = now.Add(-50 * time.Millisecond)
	exists, late, dur, waitDur, wIndex = pdc.bestOverdriveUnresolvedWorker(uws)
	if !exists || late || wIndex != 1 {
		t.Fatal("unexpected")
	}

	// now alter w2 to be late as well, we expect the worker with the lowest
	// read time to be the best one here
	uws[1].staticExpectedResolvedTime = now.Add(-100 * time.Millisecond)
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
