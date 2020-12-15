package renter

import (
	"container/heap"
	"strings"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
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
		w.newPriceTable()
		w.initJobReadQueue()
		w.staticJobReadQueue.weightedJobTime64k = float64(expectedJobTime)
		w.staticJobReadQueue.weightedJobsCompleted64k = 1
		return w
	}

	// mock 3 workers, each with different historic read times
	worker1 := mockWorker("host1", dur200MS)
	worker2 := mockWorker("host2", dur100MS)
	worker3 := mockWorker("host3", dur50MS)

	// define a list of unresolved workers, note that the expected order is
	// influenced here by `staticExpectedCompleteTime`
	now := time.Now()
	unresolvedWorkers := []*pcwsUnresolvedWorker{
		{
			staticExpectedCompleteTime: now.Add(dur100MS),
			staticWorker:               worker1,
		}, // 300ms
		{
			staticExpectedCompleteTime: now.Add(dur50MS),
			staticWorker:               worker2,
		}, // 150ms
		{
			staticExpectedCompleteTime: now.Add(dur200MS),
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
	wh := pdc.initialWorkerHeap(unresolvedWorkers)
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
		completeTime: t75MS,
		readDuration: dur50MS,
		pieces:       []uint64{0},
		cost:         pS.Mul64(10),
	} // 125
	w2 := &pdcInitialWorker{
		worker:       &worker{staticHostPubKeyStr: "w2"},
		completeTime: t100MS,
		readDuration: dur100MS,
		pieces:       []uint64{0},
		cost:         pS.Mul64(20),
	} // 200
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
	} // 175

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
	// there's not enough workers, seeing as w1 and w2 return the same pace,
	// rendering w2 unuseful.
	iws, err := pdc.createInitialWorkerSet(wh)
	if !errors.Contains(err, errNotEnoughWorkers) || iws != nil {
		t.Fatal("unexpected")
	}

	// recreate the heap but add a fourth worker, recreate the worker set, we
	// expect it to succeed as we can download min pieces
	wh = workersToHeap(w1, w2, w3, w4)
	iws, err = pdc.createInitialWorkerSet(wh)
	if err != nil {
		t.Fatal("unepected", err)
	}
	if workersToString(iws) != "w1,w3,w4" {
		t.Fatal("unepected", iws)
	}

	// recreate the heap and add another worker to the heap and recreate the
	// worker set
	wh = workersToHeap(w1, w2, w3, w4, w5)
	iws, err = pdc.createInitialWorkerSet(wh)
	if err != nil {
		t.Fatal("unepected", err)
	}
	if workersToString(iws) != "w1,w3,w5" {
		t.Fatal("unepected", workersToString(iws))
	}
}
