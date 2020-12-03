package renter

import (
	"container/heap"
	"testing"
	"time"
)

// TestProjectDownloadChunkHeap is a unit test that covers the functionality of
// the pdcWorkerHeap
func TestProjectDownloadChunkHeap(t *testing.T) {
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
