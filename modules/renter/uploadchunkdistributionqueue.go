package renter

// TODO: We can potentially re-write the queue so that we're continually
// checking each time a distribution fails whether there's a new chunk we should
// be grabbing from priority instead. Might be a little complicated to do that.

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
	// lowPriorityBumpRate is the minimum throughput as a percentage that low
	// priority traffic will have when waiting in the queue. For example, a min
	// throughput of 0.1 means that for every 1 GB of high priority traffic that
	// goes through, at least 100 MB of low priority traffic will go through.
	//
	// Raising the min throughput can negatively impact the latency for real
	// time uploads. A high rate means that users trying to upload new files
	// will often get stuck waiting for repair traffic to complete.
	//
	// If the throughput is too low, repair traffic will have no priority and is
	// at risk of starving due to lots of new upload traffic. In general, the
	// best solution for handling high repair traffic is to migrate the current
	// node to a maintenance server (that is not receiving new uploads) and put
	// a new node out for users to upload to. A higher value for this constant
	// means more lantecy for new uploads, but also means that a server can
	// handle more files overall before being rotated to maintenance.
	lowPriorityMinThroughput = 0.1

	// workerBusyThreshold is the number of jobs a worker needs to have to be
	// considered busy. A threshold of 1 for example means the worker is 'busy'
	// if it has 1 upload job or more in its queue.
	workerUploadBusyThreshold = 1

	// workerOverloadedThreshold is the number of jobs a worker needs to have to
	// be considered overloaded. A threshold of 3 for example means the worker
	// is 'overloaded' if there are 3 jobs or more in its queue.
	workerUploadOverloadedThreshold = 3
)

// uploadChunkDistributionQueue is a struct which tracks which chunks are queued
// to be distributed to workers, and serializes distribution so that one chunk
// is added to workers at a time. Distribution is controlled so that workers get
// an even balance of work and no single worker ends up with a backlog that can
// slow down the whole system.
type uploadChunkDistributionQueue struct {
	processThreadRunning bool

	priorityBuildup float64
	priorityLane    *ucdqFifo
	lowPriorityLane *ucdqFifo

	mu           sync.Mutex
	staticRenter *Renter
}

// ucdqFifo implements a fifo to use with the ucdq.
type ucdqFifo struct {
	*list.List
}

// newUCDQfifo inits a fifo for the ucdq.
func newUCDQfifo() *ucdqFifo {
	return &ucdqFifo{
		List: list.New(),
	}
}

// newUploadChunkDistributionQueue will initialize a ucdq for the renter.
func newUploadChunkDistributionQueue(r *Renter) *uploadChunkDistributionQueue {
	return &uploadChunkDistributionQueue{
		priorityLane:    newUCDQfifo(),
		lowPriorityLane: newUCDQfifo(),

		staticRenter: r,
	}
}

// Peek returns the first in the queue without removing that element from the
// queue.
func (u *ucdqFifo) Peek() *unfinishedUploadChunk {
	return u.List.Front().Value.(*unfinishedUploadChunk)
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
	// We need to hold a lock for the whole process of adding a UC.
	ucdq.mu.Lock()
	defer ucdq.mu.Unlock()

	// Since we're definitely going to add a uc to the queue, we also need to
	// make sure that a processing thread is launched to process it. If there's
	// already a processing thread running, nothing will happen. If there is not
	// a processing thread running, we need to set the thread running bool to
	// true whole still holding the ucdq lock, which is why this function is
	// deferred after the unlock.
	defer func() {
		// Check if there is a thread running to process the queue.
		if !ucdq.processThreadRunning {
			ucdq.processThreadRunning = true
			ucdq.staticRenter.tg.Launch(func() {
				ucdq.threadedProcessQueue()
			})
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
	ucdq.priorityBuildup += lowPriorityMinThroughput * float64(uc.memoryNeeded)

	// Add items from the low priority lane until there is no more buildup.
	for ucdq.lowPriorityLane.Len() > 0 && ucdq.priorityBuildup > float64(ucdq.lowPriorityLane.Peek().memoryNeeded) {
		ucdq.priorityBuildup -= float64(ucdq.lowPriorityLane.Peek().memoryNeeded)
		x := ucdq.lowPriorityLane.Pop()
		ucdq.priorityLane.PushBack(x)
	}
}

// threadedProcessQueue serializes the processing of chunks in the distribution
// queue. If there are priority chunks, it'll handle those first, and then if
// there are no chunks in the priority lane it'll handle things in the non
// priority lane. Each lane is treated like a FIFO.
//
// When de-queuing, if the priority lane is empty and chunks are being pulled
// out of the non-priority lane, the priority buildup can be reduced because
// there is no starvation of the low priority lane. Ensure that the reductions
// don't go negative.
func (ucdq *uploadChunkDistributionQueue) threadedProcessQueue() {
	for {
		// Extract the next item in the queue.
		ucdq.mu.Lock()
		// First check for the exit condition - the queue is empty. While
		// holding the lock, release the process bool and then exit.
		if ucdq.priorityLane.Len() == 0 && ucdq.lowPriorityLane.Len() == 0 {
			ucdq.processThreadRunning = false
			ucdq.mu.Unlock()
			return
		}
		// At least one uc exists in the queue. Prefer to grab the priority
		// one, if there is no priority one grab the low priority one.
		var nextUC *unfinishedUploadChunk
		if ucdq.priorityLane.Len() > 0 {
			nextUC = ucdq.priorityLane.Pop()
		} else {
			nextUC = ucdq.lowPriorityLane.Pop()
			ucdq.priorityBuildup -= float64(nextUC.memoryNeeded)
			if ucdq.priorityBuildup < 0 {
				ucdq.priorityBuildup = 0
			}
		}
		ucdq.mu.Unlock()

		// While not holding the lock but still blocking, pass the chunk off to
		// the thread that will distribute the chunk to workers.
		ucdq.staticRenter.managedDistributeChunkToWorkers(nextUC)
	}
}

// managedDistributeChunkToWorkers is a function which will block until workers
// are ready to perform upload jobs, and then will distribute the input chunk to
// the workers
func (r *Renter) managedDistributeChunkToWorkers(uc *unfinishedUploadChunk) {
	// Grab the best set of workers to receive this chunk. This function may
	// take a significant amount of time to return, as it will wait until there
	// are enough workers available to accept the chunk. This waiting pressure
	// keeps throughput high because most workers will continue to be busy all
	// the time, but it also significantly improves latency for high priority
	// chunks because the distribution queue can ensure that priority chunks
	// always get to the front of the worker line.
	workers := r.managedFindBestUploadWorkerSet(uc)

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
	// Signal to the thread that scheduled the upload that this chunk has made
	// it through the queue and it's okay to move onto the next chunk.
	close(uc.staticWorkDistributedChan)

	uc.managedUpdateDistributionTime()
	r.repairLog.Printf("Distributed chunk %v of %s to %v workers.", uc.staticIndex, uc.staticSiaPath, jobsDistributed)
	// Cleanup is required after distribution to ensure that memory is released
	// for any pieces which don't have a worker.
	r.managedCleanUpUploadChunk(uc)
}

// managedFindBestUploadWorkerSet will look through the set of available workers
// and hand-pick the workers that should be used for the upload chunk. It may
// also choose to wait until more workers are available, which means this
// function can potentially block for long periods of time.
func (r *Renter) managedFindBestUploadWorkerSet(uc *unfinishedUploadChunk) []*worker {
	for {
		// Grab the set of workers to upload. If 'finalized' is false, it means
		// that all of the good workers are already busy, and we need to wait
		// before distributing the chunk.
		workers, finalized := managedSelectWorkersForUploading(uc, r.staticWorkerPool.callWorkers())
		if finalized {
			return workers
		}

		// TODO: Use a channel instead or some sort of counter as workers finish
		// so that we know when to scan again, instead of doing this sleep
		// thing. The sleep thing will spin more than it needs to and also be
		// late sometimes.
		if !r.tg.Sleep(time.Millisecond * 25) {
			return nil
		}
	}
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
	// viable candidates for receiving work. 'busy' workers have more than 0
	// jobs but less than 5 jobs in their queue.
	//
	// Unless there are not enough jobs period
	var availableWorkers, busyWorkers, overloadedWorkers uint64
	for _, w := range workers {
		// Skip any worker that is on cooldown or is !GFU.
		cache := w.staticCache()
		w.mu.Lock()
		onCooldown, _ := w.onUploadCooldown()
		w.mu.Unlock()
		gfu := cache.staticContractUtility.GoodForUpload
		if onCooldown || !gfu {
			continue
		}

		// Count what type of worker this is. Available means the queue is
		// empty, busy means the queue has 1 or 2 items in it, and overloaded
		// means the queue has 3 or more items in it.
		//
		// We are only willing to distribute new work to busy and overloaded
		// workers.
		if w.unprocessedChunks.Len() < workerUploadBusyThreshold {
			availableWorkers++
		} else if w.unprocessedChunks.Len() < workerUploadOverloadedThreshold {
			busyWorkers++
		} else {
			overloadedWorkers++
			continue
		}
		workers[availableWorkers+busyWorkers-1] = w
	}
	// Truncate the set of workers to only include the available and the busy
	// workers.
	workers = workers[:availableWorkers+busyWorkers]

	// Distribute the upload depending on the number of pieces and the number of
	// workers. We want to handle every edge case where there are more pieces
	// than workers total, which means that waiting is not going to improve the
	// situation.
	if availableWorkers >= uint64(uc.minimumPieces) && availableWorkers+busyWorkers >= uint64(uc.piecesNeeded) {
		// This is the base success case. We have enough available workers to
		// get the chunk 'available' on the Sia network ASAP, and we have enough
		// busy workers to complete the chunk all the way.
		return workers, true
	}
	if availableWorkers >= uint64(uc.minimumPieces) && overloadedWorkers == 0 {
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
