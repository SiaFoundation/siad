package renter

import (
	"container/heap"
	"math"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestProjectDownloadChunkHeap is a unit test that covers the functionality of
// the pdcWorkerHeap
func TestProjectDownloadChunkHeap(t *testing.T) {
	t.Parallel()

	var wh pdcWorkerHeap
	if wh.Len() != 0 {
		t.Fatal("unexpected")
	}

	now := time.Now()
	tMin1 := now.Add(time.Minute)
	tMin5 := now.Add(5 * time.Minute)
	tMin10 := now.Add(10 * time.Minute)

	// add one element
	heap.Push(&wh, &pdcInitialWorker{completeTime: tMin5})
	if wh.Len() != 1 {
		t.Fatal("unexpected")
	}

	// add more elements in a way they should be popped off in a different order
	heap.Push(&wh, &pdcInitialWorker{completeTime: tMin1})
	heap.Push(&wh, &pdcInitialWorker{completeTime: tMin10})
	if wh.Len() != 3 {
		t.Fatal("unexpected")
	}

	worker := heap.Pop(&wh).(*pdcInitialWorker)
	if worker == nil || worker.completeTime != tMin1 {
		t.Fatal("unexpected")
	}
	worker = heap.Pop(&wh).(*pdcInitialWorker)
	if worker == nil || worker.completeTime != tMin5 {
		t.Fatal("unexpected")
	}
	worker = heap.Pop(&wh).(*pdcInitialWorker)
	if worker == nil || worker.completeTime != tMin10 {
		t.Fatal("unexpected")
	}
}

// TestProjectDownloadChunk_initialWorkerHeap is a small unit test that verifies
// the basic functionality of the 'initialWorkerHeap' function.
func TestProjectDownloadChunk_initialWorkerHeap(t *testing.T) {
	t.Parallel()

	// define some durations
	dur50MS := time.Duration(50 * time.Millisecond)
	dur100MS := time.Duration(100 * time.Millisecond)
	dur200MS := time.Duration(200 * time.Millisecond)

	// define a helper function that mocks a worker for a given host name, and
	// mocks the return value of 'callExpectedJobTime' on its jobreadqueue
	mockWorker := func(hostName string, expectedJobTime time.Duration) *worker {
		w := new(worker)
		w.staticHostPubKeyStr = hostName
		w.newMaintenanceState()
		w.newPriceTable()
		w.initJobReadQueue()
		w.staticJobReadQueue.weightedJobTime64k = float64(expectedJobTime)
		return w
	}

	// mock 3 workers, each with different historic read times
	worker1 := mockWorker("host1", dur200MS)
	worker2 := mockWorker("host2", dur100MS)
	worker3 := mockWorker("host3", dur50MS)

	// define a list of unresolved workers, note that the expected order is
	// influenced here by `staticExpectedResolvedTime`
	now := time.Now()
	unresolvedWorkers := []*pcwsUnresolvedWorker{
		{
			staticExpectedResolvedTime: now.Add(dur100MS),
			staticWorker:               worker1,
		}, // 300ms
		{
			staticExpectedResolvedTime: now.Add(dur50MS),
			staticWorker:               worker2,
		}, // 150ms
		{
			staticExpectedResolvedTime: now.Add(dur200MS),
			staticWorker:               worker3,
		}, // 250ms
	}

	// mock a pcws
	ec := modules.NewPassthroughErasureCoder()
	pcws := new(projectChunkWorkerSet)
	pcws.staticErasureCoder = ec

	// mock a pdc
	pdc := new(projectDownloadChunk)
	pdc.pieceLength = 1 << 16 // 64kb
	pdc.workerSet = pcws

	// create the initial worker heap and validate the order in which the
	// unresolved workers were added
	wh := pdc.initialWorkerHeap(unresolvedWorkers, 0)
	first := heap.Pop(&wh).(*pdcInitialWorker)
	if first.worker.staticHostPubKeyStr != worker2.staticHostPubKeyStr {
		t.Fatal("unexpected")
	}
	second := heap.Pop(&wh).(*pdcInitialWorker)
	if second.worker.staticHostPubKeyStr != worker3.staticHostPubKeyStr {
		t.Fatal("unexpected")
	}
	third := heap.Pop(&wh).(*pdcInitialWorker)
	if third.worker.staticHostPubKeyStr != worker1.staticHostPubKeyStr {
		t.Fatal("unexpected")
	}

	// put worker 2 on maintenance cooldown, very it's not part of the initial
	// worker heap and worker 3 took its place
	worker2.staticMaintenanceState.cooldownUntil = time.Now().Add(time.Minute)
	wh = pdc.initialWorkerHeap(unresolvedWorkers, 0)
	first = heap.Pop(&wh).(*pdcInitialWorker)
	if first.worker.staticHostPubKeyStr != worker3.staticHostPubKeyStr {
		t.Fatal("unexpected")
	}

	// make the read estimates for worker 3 return 0, verify it's not part of
	// initial worker heap and worker 1 took its place
	worker3.staticJobReadQueue.weightedJobTime64k = 0
	wh = pdc.initialWorkerHeap(unresolvedWorkers, 0)
	first = heap.Pop(&wh).(*pdcInitialWorker)
	if first.worker.staticHostPubKeyStr != worker1.staticHostPubKeyStr {
		t.Fatal("unexpected")
	}

	// manually manipulate the resolve time of worker 1 to be 800ms in the
	// past, we expect the complete time to be one second in the future as the
	// worker's expected read estimate was 200ms and we add the amount of time
	// the worker is late resolving
	unresolvedWorkers[0].staticExpectedResolvedTime = time.Now().Add(-800 * time.Millisecond)
	wh = pdc.initialWorkerHeap(unresolvedWorkers, 0)
	first = heap.Pop(&wh).(*pdcInitialWorker)
	completeTimeInS := math.Round(time.Until(first.completeTime).Seconds())
	if completeTimeInS != 1 {
		t.Fatal("unexpected", completeTimeInS, time.Until(first.completeTime))
	}

	// pass in an unresolved worker penalty, this should again be reflected in
	// the complete time
	wh = pdc.initialWorkerHeap(unresolvedWorkers, time.Second)
	first = heap.Pop(&wh).(*pdcInitialWorker)
	completeTimeInS = math.Round(time.Until(first.completeTime).Seconds())
	if completeTimeInS != 2 {
		t.Fatal("unexpected", completeTimeInS, time.Until(first.completeTime))
	}

	// manually manipulate the cooldown for worker 1's jobreadqueue, this should
	// skip the worker
	worker1.staticJobReadQueue.cooldownUntil = time.Now().Add(time.Second)
	wh = pdc.initialWorkerHeap(unresolvedWorkers, 0)
	if wh.Len() != 0 {
		t.Fatal("unexpected", wh.Len())
	}

	// NOTE: unfortunately this unit test does not verify whether resolved
	// workers are properly added to the heap, it proved nearly impossible to
	// mock all of the state required to do so manually (due to
	// `managedAsyncReady`)
}

// TestCreateInitialWorkerSet is a unit test that verifies the functionality of
func TestProjectDownloadChunk_createInitialWorkerSet(t *testing.T) {
	t.Parallel()

	// workersToString is a helper function that outputs the worker's host
	// pubkey strings for easy output comparison
	workersToString := func(iws []*pdcInitialWorker) string {
		var workers []string
		for _, worker := range iws {
			if worker != nil {
				workers = append(workers, worker.worker.staticHostPubKeyStr)
			}
		}
		return strings.Join(workers, ",")
	}

	// workersToHeap is a helper function that takes the given workers and add
	// them to the heap
	workersToHeap := func(workers ...*pdcInitialWorker) pdcWorkerHeap {
		wh := new(pdcWorkerHeap)
		for _, iw := range workers {
			heap.Push(wh, iw)
		}
		return *wh
	}

	// create some helper variables
	dur5MS := 5 * time.Millisecond
	dur15MS := 15 * time.Millisecond
	dur50MS := 50 * time.Millisecond
	dur75MS := 75 * time.Millisecond
	dur100MS := 100 * time.Millisecond
	dur125MS := 125 * time.Millisecond
	dur150MS := 150 * time.Millisecond
	dur175MS := 175 * time.Millisecond

	now := time.Now()
	t50MS := now.Add(dur50MS)
	t75MS := now.Add(dur75MS)
	t100MS := now.Add(dur100MS)
	t125MS := now.Add(dur125MS)
	t150MS := now.Add(dur150MS)

	pS := types.SiacoinPrecision.MulFloat(1e-12)

	// create a couple of workers
	w1 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w1"},
		completeTime: t100MS,
		readDuration: dur100MS,
		pieces:       []uint64{0},
		cost:         pS.Mul64(10),
	} // 200
	w2 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w2"},
		completeTime: t75MS,
		readDuration: dur50MS,
		pieces:       []uint64{0},
		cost:         pS.Mul64(20),
	} // 175
	w3 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w3"},
		completeTime: t50MS,
		readDuration: dur50MS,
		pieces:       []uint64{1},
		cost:         pS.Mul64(30),
	} // 100
	w4 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w4"},
		completeTime: t125MS,
		readDuration: dur175MS,
		pieces:       []uint64{2},
		cost:         pS.Mul64(40),
	} // 300
	w5 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w5"},
		completeTime: t150MS,
		readDuration: dur15MS,
		pieces:       []uint64{3},
		cost:         pS.Mul64(39), // undercut w4
	} // 165
	w6 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w6"},
		completeTime: t50MS,
		readDuration: dur5MS, // super fast
		pieces:       []uint64{3, 4},
		cost:         pS.Mul64(50),
	} // 55

	// create a heap and add the first three workers
	wh := workersToHeap(w1, w2, w3)

	// create an erasure coder, we use 3 data pieces to cover more of the
	// algorithm.
	ec, err := modules.NewRSSubCode(3, 12, crypto.SegmentSize)
	if err != nil {
		t.Fatal(err)
	}

	// mock a pcws
	pcws := new(projectChunkWorkerSet)
	pcws.staticErasureCoder = ec

	// mock a pdc
	pdc := new(projectDownloadChunk)
	pdc.workerSet = pcws
	pdc.pricePerMS = types.SiacoinPrecision.MulFloat(1e-12) // pS

	// create an initial worker set, we expect this to fail due to the fact
	// there's not enough workers, seeing as w1 and w2 return the same piece,
	// rendering w1 unuseful.
	iws, err := pdc.createInitialWorkerSet(wh)
	if !errors.Contains(err, errNotEnoughWorkers) || iws != nil {
		t.Fatal("unexpected")
	}

	// add a fourth worker, we expect it to succeed now and return an initial
	// worker set that can download min pieces
	wh = workersToHeap(w1, w2, w3, w4)
	iws, err = pdc.createInitialWorkerSet(wh)
	if err != nil {
		t.Fatal("unexpected", err)
	}
	if workersToString(iws) != "w1,w3,w4" {
		t.Fatal("unexpected", workersToString(iws))
	}

	// add another worker, undercutting w4 in price
	wh = workersToHeap(w1, w2, w3, w4, w5)
	iws, err = pdc.createInitialWorkerSet(wh)
	if err != nil {
		t.Fatal("unexpected", err)
	}
	if workersToString(iws) != "w1,w3,w5" {
		t.Fatal("unexpected", workersToString(iws))
	}

	// add another worker, it's super fast and able to download two pieces in
	// under the time it takes w3 to download 1
	wh = workersToHeap(w1, w2, w3, w4, w5, w6)
	iws, err = pdc.createInitialWorkerSet(wh)
	if err != nil {
		t.Fatal("unexpected", err)
	}
	if workersToString(iws) != "w2,w3,w6" {
		t.Fatal("unexpected", workersToString(iws))
	}
}

// TestProjectDownloadGouging checks that `checkProjectDownloadGouging` is
// correctly detecting price gouging from a host.
func TestProjectDownloadGouging(t *testing.T) {
	t.Parallel()

	// allowance contains only the fields necessary to test the price gouging
	hes := modules.DefaultHostExternalSettings()
	allowance := modules.Allowance{
		Funds:                     types.SiacoinPrecision.Mul64(1e3),
		MaxDownloadBandwidthPrice: hes.DownloadBandwidthPrice.Mul64(10),
		MaxUploadBandwidthPrice:   hes.UploadBandwidthPrice.Mul64(10),
	}

	// verify happy case
	pt := newDefaultPriceTable()
	err := checkProjectDownloadGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	// verify max download bandwidth price gouging
	pt = newDefaultPriceTable()
	pt.DownloadBandwidthCost = allowance.MaxDownloadBandwidthPrice.Add64(1)
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "download bandwidth price") {
		t.Fatalf("expected download bandwidth price gouging error, instead error was '%v'", err)
	}

	// verify max upload bandwidth price gouging
	pt = newDefaultPriceTable()
	pt.UploadBandwidthCost = allowance.MaxUploadBandwidthPrice.Add64(1)
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "upload bandwidth price") {
		t.Fatalf("expected upload bandwidth price gouging error, instead error was '%v'", err)
	}

	// update the expected download to be non zero and verify the default prices
	allowance.ExpectedDownload = 1 << 30 // 1GiB
	pt = newDefaultPriceTable()
	err = checkProjectDownloadGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	// verify gouging of MDM related costs, in order to verify if gouging
	// detection kicks in we need to ensure the cost of executing enough PDBRs
	// to fulfil the expected download exceeds the allowance

	// we do this by maxing out the upload and bandwidth costs and setting all
	// default cost components to 250 pS, note that this value is arbitrary,
	// setting those costs at 250 pS simply proved to push the price per PDBR
	// just over the allowed limit.
	//
	// Cost breakdown:
	// - cost per PDBR 266.4 mS
	// - total cost to fulfil expected download 4.365 KS
	// - reduced cost after applying downloadGougingFractionDenom: 1.091 KS
	// - exceeding the allowance of 1 KS, which is what we are after
	pt.UploadBandwidthCost = allowance.MaxUploadBandwidthPrice
	pt.DownloadBandwidthCost = allowance.MaxDownloadBandwidthPrice
	pS := types.SiacoinPrecision.MulFloat(1e-12)
	pt.InitBaseCost = pt.InitBaseCost.Add(pS.Mul64(250))
	pt.ReadBaseCost = pt.ReadBaseCost.Add(pS.Mul64(250))
	pt.MemoryTimeCost = pt.MemoryTimeCost.Add(pS.Mul64(250))
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	// verify these checks are ignored if the funds are 0
	allowance.Funds = types.ZeroCurrency
	err = checkProjectDownloadGouging(pt, allowance)
	if err != nil {
		t.Fatal("unexpected price gouging failure", err)
	}

	allowance.Funds = types.SiacoinPrecision.Mul64(1e3) // reset

	// verify bumping every individual cost component to an insane value results
	// in a price gouging error
	pt = newDefaultPriceTable()
	pt.InitBaseCost = types.SiacoinPrecision.Mul64(100)
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.ReadBaseCost = types.SiacoinPrecision
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.ReadLengthCost = types.SiacoinPrecision
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}

	pt = newDefaultPriceTable()
	pt.MemoryTimeCost = types.SiacoinPrecision
	err = checkProjectDownloadGouging(pt, allowance)
	if err == nil || !strings.Contains(err.Error(), "combined PDBR pricing of host yields") {
		t.Fatalf("expected PDBR price gouging error, instead error was '%v'", err)
	}
}
