package renter

import (
	"io/ioutil"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/siatest/dependencies"
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
// managedSelectWorkersForUploading and ensure that all of the logic is running as
// expected.
func TestManagedCheckForUploadWorkers(t *testing.T) {
	// Test the blank case. Because there are no workers, the function should
	// return a blank set of workers and indicate that the chunk is okay to
	// distribute. We don't want the upload loop freezing if there are no
	// workers.
	uc := new(unfinishedUploadChunk)
	var inputWorkers []*worker
	workers, finalized := managedSelectWorkersForUploading(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}
	// Give the UC some minpieces and needed pieces.
	uc.staticPiecesNeeded = 1
	uc.staticMinimumPieces = 1
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}
	uc.staticPiecesNeeded = 2
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
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
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
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
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
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
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
	if len(workers) != 1 {
		t.Fatal("bad")
	}
	if !finalized {
		t.Fatal("bad")
	}

	// Test what happens when there is an overloaded worker that could be busy.
	inputWorkers = append(inputWorkers, newOverloadedWorker())
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
	if workers != nil {
		t.Fatal("bad")
	}
	if finalized {
		t.Fatal("bad")
	}
	// Now check what happens when it is busy. There are enough available
	// workers to make the chunk available, and enough busy workers to finish
	// the chunk, so it should pass.
	for i := 0; i < workerUploadOverloadedThreshold-workerUploadBusyThreshold; i++ {
		inputWorkers[1].unprocessedChunks.Pop()
	}
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
	if len(workers) != 2 {
		t.Fatal("bad", len(workers))
	}
	if !finalized {
		t.Fatal("bad")
	}
	// Change the number of staticPiecesNeeded in the chunk to 3, so that we now
	// have enough available workers to make the chunk available but not enough
	// busy workers (or workers at all) to make the chunk complete. This should
	// succeed.
	workers, finalized = managedSelectWorkersForUploading(uc, inputWorkers)
	if len(workers) != 2 {
		t.Fatal("bad", len(workers))
	}
	if !finalized {
		t.Fatal("bad")
	}
}

// TestAddUploadChunkCritial is a test that triggers the critical within
// callAddUploadChunk.
func TestAddUploadChunkCritical(t *testing.T) {
	log, _ := persist.NewLogger(ioutil.Discard)
	r := &Renter{
		deps: &dependencies.DependencyDelayChunkDistribution{},
		log:  log,
	}
	ucdq := newUploadChunkDistributionQueue(r)

	chunk := func(priority bool, memoryNeeded uint64) *unfinishedUploadChunk {
		return &unfinishedUploadChunk{
			staticPriority:     priority,
			staticMemoryNeeded: memoryNeeded,
		}
	}

	// Push low priority chunk with 10 bytes twice. The first one will be
	// processed right away while the second one will stay in the lane waiting
	// for the first one to be distributed.
	ucdq.callAddUploadChunk(chunk(false, 10))
	ucdq.callAddUploadChunk(chunk(false, 10))

	ucdq.mu.Lock()
	if ucdq.lowPriorityLane.Len() == 0 {
		t.Fatal("low prio lane should contain chunks", ucdq.lowPriorityLane.Len())
	}
	ucdq.mu.Unlock()

	// Push high priority chunk with 150 bytes. This should bump a low prio
	// chunk to the high prio lane and leave a non-zero buildup after the first
	// low prio chunk is distributed.
	ucdq.callAddUploadChunk(chunk(true, 150))

	// Wait for the low priority lane to empty itself.
	err := build.Retry(1000, 10*time.Millisecond, func() error {
		ucdq.mu.Lock()
		defer ucdq.mu.Unlock()
		if ucdq.lowPriorityLane.Len() == 0 {
			return nil
		}
		return errors.New("low prio lane not empty")
	})
	if err != nil {
		t.Fatal(err)
	}

	// Push a high prio chunk. This should not cause a panic.
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("expected no panic but got %v", r)
			}
		}()
		ucdq.callAddUploadChunk(chunk(true, 1))
	}()
}
