package renter

import (
	"container/list"
	"sync"
	"time"
)

// uploadchunkdistributionqueue.go creates a queue for distributing upload
// chunks to workers. The queue has two lanes, one for priority upload work and
// one for low priority upload work. Priority upload work always goes first if
// it's available, but to ensure that low priority work gets at least a minimal
// amount of throughput we will bump low priority work in to the priority work
// queue if too much priority work gets scheduled while the low priority work is
// waiting.

const (
	// uploadChunkDistirbutionBackoff dictates the amount of time that the
	// distributor will sleep after determining that a chunk is not ready to be
	// distributed because too many workers are busy.
	uploadChunkDistributionBackoff = time.Millisecond * 25

	// lowPriorityMinThroughput is the minimum throughput as a ratio that low
	// priority traffic will have when waiting in the queue. For example, a min
	// throughput multiplier of 10 means that for every 1 GB of high priority
	// traffic that gets queued, at least 100 MB of low priority traffic will be
	// promoted to high priority traffic.
	//
	// Raising the min throughput can negatively impact the latency for real
	// time uploads. A high rate means that users trying to upload new files
	// will often get stuck waiting for repair traffic that was bumped in
	// priority.
	//
	// If the throughput is too low, repair traffic will have no priority and is
	// at risk of starving due to lots of new upload traffic. In general, the
	// best solution for handling high repair traffic is to migrate the current
	// node to a maintenance server (that is not receiving new uploads) and have
	// users upload to a fresh node that has very little need of repair traffic.
	// Increasing the lowPriorityMinThroughput will increase the total amount of
	// data that a node can maintain at the cost of latency for new uploads.
	lowPriorityMinThroughputMultiplier = 10 // 10%

	// workerUploadBusyThreshold is the number of jobs a worker needs to have to
	// be considered busy. A threshold of 1 for example means the worker is
	// 'busy' if it has 1 upload job or more in its queue.
	workerUploadBusyThreshold = 1

	// workerUploadOverloadedThreshold is the number of jobs a worker needs to
	// have to be considered overloaded. A threshold of 3 for example means the
	// worker is 'overloaded' if there are 3 jobs or more in its queue.
	workerUploadOverloadedThreshold = 3
)

// uploadChunkDistributionQueue is a struct which tracks which chunks are queued
// to be distributed to workers, and serializes distribution so that one chunk
// is added to workers at a time. Distribution is controlled so that workers get
// an even balance of work and no single worker ends up with a backlog that can
// slow down the whole system.
type uploadChunkDistributionQueue struct {
	processThreadRunning bool

	priorityBuildup uint64
	priorityLane    *ucdqFifo
	lowPriorityLane *ucdqFifo

	mu           sync.Mutex
	staticRenter *Renter
}

// ucdqFifo implements a fifo to use with the ucdq.
type ucdqFifo struct {
	*list.List
}

// newUploadChunkDistributionQueue will initialize a ucdq for the renter.
func newUploadChunkDistributionQueue(r *Renter) *uploadChunkDistributionQueue {
	return &uploadChunkDistributionQueue{
		priorityLane:    newUCDQfifo(),
		lowPriorityLane: newUCDQfifo(),

		staticRenter: r,
	}
}

// newUCDQfifo inits a fifo for the ucdq.
func newUCDQfifo() *ucdqFifo {
	return &ucdqFifo{
		List: list.New(),
	}
}

// Pop removes and returns the first element in the fifo, removing that element
// from the queue.
func (u *ucdqFifo) Pop() *unfinishedUploadChunk {
	mr := u.Front()
	if mr == nil {
		return nil
	}
	return u.List.Remove(mr).(*unfinishedUploadChunk)
}

// callAddUploadChunk will add an unfinished upload chunk to the queue. The
// chunk will be put into a lane based on whether the memory was requested with
// priority or not.
func (ucdq *uploadChunkDistributionQueue) callAddUploadChunk(uc *unfinishedUploadChunk) {
	// We need to hold a lock for the whole process of adding an upload chunk.
	ucdq.mu.Lock()
	defer ucdq.mu.Unlock()

	// Since we're definitely going to add a chunk to the queue, we also need to
	// make sure that a processing thread is launched to process it. If there's
	// already a processing thread running, nothing will happen. If there is not
	// a processing thread running, we need to set the thread running bool to
	// true whole still holding the ucdq lock, which is why this function is
	// deferred to run before the unlock.
	defer func() {
		// Check if there is a thread running to process the queue.
		if !ucdq.processThreadRunning {
			ucdq.processThreadRunning = true
			ucdq.staticRenter.tg.Launch(ucdq.threadedProcessQueue)
		}
	}()

	// If the chunk is not a priority chunk, put it in the low priority lane.
	if !uc.staticPriority {
		ucdq.lowPriorityLane.PushBack(uc)
		return
	}

	// If the chunk is a priority chunk, add it in the priority lane and then
	// determine whether a low priority chunk needs to be bumped to the priority
	// lane.
	//
	// The bumping happens when a priority chunk is added to ensure that low
	// priority chunks are evenly distributed throughout the high priority
	// queue, and that a sudden influx of high priority chunks doesn't mean that
	// low priority chunks will have to wait a long time even if they get
	// bumped.
	ucdq.priorityLane.PushBack(uc)
	if ucdq.lowPriorityLane.Len() == 0 {
		// No need to worry about priority buildup if there is nothing waiting
		// in the low priority lane.
		return
	}
	// Tally up the new buildup caused by this new priority chunk.
	ucdq.priorityBuildup += uc.staticMemoryNeeded

	// Add items from the low priority lane as long as there is enough buildup
	// to justify bumping them.
	for x := ucdq.lowPriorityLane.Pop(); x != nil; x = ucdq.lowPriorityLane.Pop() {
		// If there is buildup, add the item.
		needed := x.staticMemoryNeeded * lowPriorityMinThroughputMultiplier
		if ucdq.priorityBuildup >= needed {
			ucdq.priorityBuildup -= needed
			ucdq.priorityLane.PushBack(x)
			continue
		}
		// Otherwise return the element. We are done.
		ucdq.lowPriorityLane.PushFront(x)
		break
	}
	// If all low priority items were bumped into the high priority lane, the
	// buildup can be cleared out.
	if ucdq.lowPriorityLane.Len() == 0 {
		ucdq.priorityBuildup = 0
	}
}

// threadedProcessQueue serializes the processing of chunks in the distribution
// queue. If there are priority chunks, it'll handle those first, and then if
// there are no chunks in the priority lane it'll handle things in the low
// priority lane. Each lane is treated like a FIFO.
//
// When things are being pulled out of the low priority lane, the priority
// buildup can be reduced because the low priority lane is not starving.
//
// The general structure of this function is to pull a chunk out of a queue,
// then try to distribute the chunk. The distributor function may determine that
// the workers are not ready to process the chunk yet. If the distributor
// function indicates that a chunk was not distributed, the chunk should go back
// into the queue it came out of. Then on the next iteration, we will grab the
// highest priority chunk.
func (ucdq *uploadChunkDistributionQueue) threadedProcessQueue() {
	for {
		// Check whether the renter has shut down, return immediately if so.
		select {
		case <-ucdq.staticRenter.tg.StopChan():
			return
		default:
		}

		// Extract the next item in the queue.
		ucdq.mu.Lock()
		// First check for the exit condition - the queue is empty. While
		// holding the lock, release the process bool and then exit.
		if ucdq.priorityLane.Len() == 0 && ucdq.lowPriorityLane.Len() == 0 {
			ucdq.processThreadRunning = false
			ucdq.mu.Unlock()
			return
		}
		// At least one uc exists in the queue. Prefer to grab the priority one,
		// if there is no priority one grab the low priority one. We need to
		// remember which lane the uc came from because we may need to put it
		// back into that lane later.
		var nextUC *unfinishedUploadChunk
		var priority bool
		if ucdq.priorityLane.Len() > 0 {
			nextUC = ucdq.priorityLane.Pop()
			priority = true
		} else {
			nextUC = ucdq.lowPriorityLane.Pop()
			priority = false
		}
		ucdq.mu.Unlock()

		var distributed bool
		if ucdq.staticRenter.deps.Disrupt("DelayChunkDistribution") {
			// Simulate chunk distribution but don't actually distribute it.
			time.Sleep(time.Second)
			distributed = true
		} else {
			// While not holding the lock but still blocking, pass the chunk off to
			// the thread that will distribute the chunk to workers. This call can
			// fail. If the call failed, the chunk should be re-inserted into the
			// front of the low prior heap IFF the chunk was a low prio chunk.
			distributed = ucdq.staticRenter.managedDistributeChunkToWorkers(nextUC)
		}

		// If the chunk was not distributed, we want to block briefly to give
		// the workers time to process the items in their queue. The only reason
		// that a chunk will not be distributed is because workers have too much
		// work in their queue already.
		if !distributed {
			// NOTE: This could potentially be improved by switching it to a channel
			// that waits for new chunks to appear or waits for busy/overloaded workers
			// to report a better state. We opted not to do that here because 25ms is
			// not a huge penalty to pay and there's a fair amount of complexity
			// involved in switching to a better solution.
			ucdq.staticRenter.tg.Sleep(uploadChunkDistributionBackoff)
		}
		if distributed && priority {
			// If the chunk was distributed successfully and we pulled the chunk
			// from the priority lane, there is nothing more to do.
			continue
		}
		if distributed && !priority {
			// If the chunk was distributed successfully and we pulled the chunk
			// from the low priority lane, we need to subtract from the priority
			// buildup as the low priority lane has made progress.
			ucdq.mu.Lock()
			needed := nextUC.staticMemoryNeeded * lowPriorityMinThroughputMultiplier
			if ucdq.priorityBuildup < needed {
				ucdq.priorityBuildup = 0
			} else {
				ucdq.priorityBuildup -= needed
			}
			ucdq.mu.Unlock()
			continue
		}
		if !distributed && priority {
			// If the chunk was not distributed, we need to push it back to the
			// front of the priority lane and then cycle again.
			ucdq.mu.Lock()
			ucdq.priorityLane.PushFront(nextUC)
			ucdq.mu.Unlock()
			continue
		}
		if !distributed && !priority {
			// If the chunk was not distributed, push it back into the front of
			// the low priority lane. The next iteration may grab a high
			// priority chunk if a new high prio chunk has appeared while we
			// were checking on this chunk.
			ucdq.mu.Lock()
			ucdq.lowPriorityLane.PushFront(nextUC)
			ucdq.mu.Unlock()
			continue
		}
		panic("missing case, this code should not be reachable")
	}
}

// managedDistributeChunkToWorkers is a function which will attempt to
// distribute the chunk to workers for upload. If the distribution is
// successful, it will return true. If the distribution is not successful, it
// will return false, indicating that distribution needs to be retried.
func (r *Renter) managedDistributeChunkToWorkers(uc *unfinishedUploadChunk) bool {
	// Grab the best set of workers to receive this chunk. This function may
	// take a significant amount of time to return, as it will wait until there
	// are enough workers available to accept the chunk. This waiting pressure
	// keeps throughput high because most workers will continue to be busy all
	// the time, but it also significantly improves latency for high priority
	// chunks because the distribution queue can ensure that priority chunks
	// always get to the front of the worker line.
	workers, finalized := r.managedFindBestUploadWorkerSet(uc)
	if !finalized {
		return false
	}

	// Give the chunk to each worker, marking the number of workers that have
	// received the chunk. Only count the worker if the worker's upload queue
	// accepts the job.
	uc.managedIncreaseRemainingWorkers(len(workers))
	jobsDistributed := 0
	for _, w := range workers {
		if w.callQueueUploadChunk(uc) {
			jobsDistributed++
		}
	}

	uc.managedUpdateDistributionTime()
	r.repairLog.Printf("Distributed chunk %v of %s to %v workers.", uc.staticIndex, uc.staticSiaPath, jobsDistributed)
	// Cleanup is required after distribution to ensure that memory is released
	// for any pieces which don't have a worker.
	r.managedCleanUpUploadChunk(uc)
	return true
}

// managedFindBestUploadWorkerSet will look through the set of available workers
// and hand-pick the workers that should be used for the upload chunk. It may
// also choose to wait until more workers are available, which means this
// function can potentially block for long periods of time.
func (r *Renter) managedFindBestUploadWorkerSet(uc *unfinishedUploadChunk) ([]*worker, bool) {
	// Grab the set of workers to upload. If 'finalized' is false, it means
	// that all of the good workers are already busy, and we need to wait
	// before distributing the chunk.
	workers, finalized := managedSelectWorkersForUploading(uc, r.staticWorkerPool.callWorkers())
	if finalized {
		return workers, true
	}
	return nil, false
}

// managedSelectWorkersForUploading is a function that will select workers to be
// used in uploading a chunk to the network. This function can fail if there are
// not enough workers that are ready to take on more work, in which case the
// caller needs to wait before trying again.
//
// This function is meant to only be called by 'managedFindBestUploadWorkerSet',
// which handles the retry mechanism for you. The functions are split up this
// way to make the retry logic easier to understand.
func managedSelectWorkersForUploading(uc *unfinishedUploadChunk, workers []*worker) ([]*worker, bool) {
	// Scan through the workers and determine how many workers have available
	// slots to upload. Available workers and busy workers are both counted as
	// viable candidates for receiving work.
	var availableWorkers, busyWorkers, overloadedWorkers uint64
	for _, w := range workers {
		// Skip any worker that is on cooldown or is !GFU.
		cache := w.staticCache()
		w.mu.Lock()
		onCooldown, _ := w.onUploadCooldown()
		numUnprocessedChunks := w.unprocessedChunks.Len()
		w.mu.Unlock()
		gfu := cache.staticContractUtility.GoodForUpload
		if onCooldown || !gfu {
			continue
		}

		// Count the worker by status. A worker is 'available', 'busy', or
		// 'overloaded' depending on how many jobs it has in its upload queue.
		// Only available and busy workers are candidates to receive the
		// unfinished chunk.
		if numUnprocessedChunks < workerUploadBusyThreshold {
			availableWorkers++
		} else if numUnprocessedChunks < workerUploadOverloadedThreshold {
			busyWorkers++
		} else {
			overloadedWorkers++
			continue
		}
		workers[availableWorkers+busyWorkers-1] = w
	}
	// Truncate the set of workers to only include the available and busy
	// workers that were added to the front of the queue while counting workers.
	workers = workers[:availableWorkers+busyWorkers]

	// Distribute the upload depending on the number of pieces and the number of
	// workers. We want to handle every edge case where there are more pieces
	// than workers total, which means that waiting is not going to improve the
	// situation.
	if availableWorkers >= uint64(uc.staticMinimumPieces) && availableWorkers+busyWorkers >= uint64(uc.staticPiecesNeeded) {
		// This is the base success case. We have enough available workers to
		// get the chunk 'available' on the Sia network ASAP, and we have enough
		// busy workers to complete the chunk all the way.
		return workers, true
	}
	if availableWorkers >= uint64(uc.staticMinimumPieces) && overloadedWorkers == 0 {
		// This is an edge case where there are no overloaded workers, and there
		// are enough available workers to make the chunk available on the Sia
		// network. Because there are no overloaded workers, waiting longer is
		// not going to allow us to make more progress, so we need to accept
		// this chunk as-is.
		return workers, true
	}
	if overloadedWorkers == 0 && busyWorkers == 0 {
		// This is the worst of the success cases. It means we don't even have
		// enough workers to make the chunk available on the network, but all
		// the workers that we do have are available. Even though this is a bad
		// place to be, the right thing to do is move forward with the upload.
		return workers, true
	}

	// In all other cases, we should wait until either some busy workers have
	// processed enough chunks to become available workers, or until some
	// overloaded workers have processed enough chunks to become busy workers,
	// or both.
	return nil, false
}
