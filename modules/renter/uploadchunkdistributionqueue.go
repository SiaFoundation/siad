package renter

// TODO: Shift all arrays to lists so that we aren't abusing memory so much.

import (
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
)

// uploadChunkDistributionQueue is a struct which tracks which chunks are queued
// to be distributed to workers, and serializes distribution so that one chunk
// is added to workers at a time. Distribution is controlled so that workers get
// an even balance of work and no single worker ends up with a backlog that can
// slow down the whole system.
type uploadChunkDistributionQueue struct {
	processThreadRunning bool

	priorityBuildup float64
	priorityLane    []*unfinishedUploadChunk
	lowPriorityLane []*unfinishedUploadChunk

	mu           sync.Mutex
	staticRenter *Renter
}

// newUploadChunkDistributionQueue will initialize a ucdq for the renter.
func newUploadChunkDistributionQueue(r *Renter) *uploadChunkDistributionQueue {
	return &uploadChunkDistributionQueue{
		staticRenter: r,
	}
}

// addUC will add an unfinished upload chunk to the queue. The chunk will be put
// into a lane based on whether the memory was requested with priority or not.
func (ucdq *uploadChunkDistributionQueue) addUC(uc *unfinishedUploadChunk) {
	// We need to hold a lock for the whole process of adding a UC.
	// defer ucdq.threadedProcessQueue()
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
		ucdq.lowPriorityLane = append(ucdq.lowPriorityLane, uc)
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
	ucdq.priorityLane = append(ucdq.priorityLane, uc)
	ucdq.priorityBuildup += lowPriorityMinThroughput * float64(uc.memoryNeeded)

	// Add items from the low priority lane until there is no more buildup.
	for len(ucdq.lowPriorityLane) > 0 && ucdq.priorityBuildup > float64(ucdq.lowPriorityLane[0].memoryNeeded) {
		ucdq.priorityBuildup -= float64(ucdq.lowPriorityLane[0].memoryNeeded)
		ucdq.priorityLane = append(ucdq.priorityLane, uc)
		ucdq.lowPriorityLane = ucdq.lowPriorityLane[1:]
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
		if len(ucdq.priorityLane) == 0 && len(ucdq.lowPriorityLane) == 0 {
			ucdq.processThreadRunning = false
			ucdq.mu.Unlock()
			return
		}
		// At least one uc exists in the queue. Prefer to grab the priority
		// one, if there is no priority one grab the low priority one.
		var nextUC *unfinishedUploadChunk
		if len(ucdq.priorityLane) > 0 {
			nextUC = ucdq.priorityLane[0]
			ucdq.priorityLane = ucdq.priorityLane[1:]
		} else {
			nextUC = ucdq.lowPriorityLane[0]
			ucdq.priorityBuildup -= float64(ucdq.lowPriorityLane[0].memoryNeeded)
			ucdq.lowPriorityLane = ucdq.lowPriorityLane[1:]
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
		workers, finalized := r.managedCheckForUploadWorkers(uc)
		if finalized {
			return workers
		}
		// TODO: use tg to make this a soft sleep.
		//
		// TODO: Use a channel instead or some sort of counter as workers finish
		// so that we know when to scan again, instead of doing this sleep
		// thing.
		time.Sleep(time.Millisecond * 25)
	}
}

// managedCheckForUploadWorkers will scan through the list of upload workers and
// determine if there is a good set for uploading. If there is a good set for
// uploading, that set will be returned with the finalized flag set to 'true',
// indicating that this set of workers should be treated as the best we can do.
// If there is not a good set, and a better set will likely appear after waiting
// for some of the current workers to process their queue further, 'false' will
// be returned.
func (r *Renter) managedCheckForUploadWorkers(uc *unfinishedUploadChunk) ([]*worker, bool) {
	workers := r.staticWorkerPool.callWorkers()

	// Scan through the workers and determine how many workers have available
	// slots to uploading.

	// TODO: Actually filter for a good set of workers LOL.
	return r.staticWorkerPool.callWorkers(), true
}
