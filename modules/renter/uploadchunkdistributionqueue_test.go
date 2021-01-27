package renter

import (
	"sync/atomic"
	"testing"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// newOverloadedWorker will return a worker that is overloaded.
func newOverloadedWorker() *worker {
	// Create and initialize a barebones worker.
	w := new(worker)
	cache := &workerCache{
		staticContractUtility: modules.ContractUtility{
			GoodForUpload: true,
		},
	}
	ptr := unsafe.Pointer(cache)
	atomic.StorePointer(&w.atomicCache, ptr)
	w.unprocessedChunks = newUploadChunks()

	for i := 0; i < workerUploadOverloadedThreshold; i++ {
		w.unprocessedChunks.PushBack(new(unfinishedUploadChunk))
	}
	return w
}

// TestManagedCheckForUploadWorkers will probe the various edge cases of
// managedCheckForUploadWorkers and ensure that all of the logic is running as
// expected.
func TestManagedCheckForUploadWorkers(t *testing.T) {
	// Test the blank case. Because there are no workers, the function should
	// return a blank set of workers and indicate that the chunk is okay to
	// distribute. We don't want the upload loop freezing if there are no
	// workers.
	uc := new(unfinishedUploadChunk)
	var inputWorkers []*worker
	workers, finalized := managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}
	// Give the UC some minpieces and needed pieces.
	uc.piecesNeeded = 1
	uc.minimumPieces = 1
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}
	uc.piecesNeeded = 2
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}

	// Test the use case where there are not enough available workers, but there
	// are enough overloaded workers. This should result in finalized being
	// 'false', as we want to wait for the overloaded workers to finish
	// processing their chunks and become available.
	inputWorkers = append(inputWorkers, newOverloadedWorker())
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if finalized {
		t.Fatal("bad")
	}

	// Test the case where the only worker is busy
	for i := 0; i < workerUploadOverloadedThreshold-workerUploadBusyThreshold; i++ {
		inputWorkers[0].unprocessedChunks.Pop()
	}
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if finalized {
		t.Fatal("bad")
	}

	// Test the case where the only worker is available. Since the only worker
	// is available, the chunk should be good to go.
	for i := 0; i < workerUploadBusyThreshold; i++ {
		inputWorkers[0].unprocessedChunks.Pop()
	}
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if len(workers) != 1 {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}

	// Test what happens when there is an overloaded worker that could be busy.
	inputWorkers = append(inputWorkers, newOverloadedWorker())
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if finalized {
		t.Fatal("bad")
	}
	// Now check what happens when it is busy. There are enough available
	// workers to make the chunk available, and enough busy workers to finish
	// the chunk, so it should pass.
	println("oy")
	for i := 0; i < workerUploadOverloadedThreshold-workerUploadBusyThreshold; i++ {
		inputWorkers[1].unprocessedChunks.Pop()
	}
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if len(workers) != 2 {
		t.Fatal("bad", len(workers))
	}
	if !finalized {
		t.Fatal("bad")
	}
	// Change the number of piecesNeeded in the chunk to 3, so that we now have
	// enough available workers to make the chunk available but not enough busy
	// workers (or workers at all) to make the chunk complete. This should
	// succeed.
	workers, finalized = managedCheckForUploadWorkers(uc, inputWorkers)
	if len(workers) != 2 {
		t.Fatal("bad", len(workers))
	}
	if !finalized {
		t.Fatal("bad")
	}
}
