package renter

import (
	"container/heap"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
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
