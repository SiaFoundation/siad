package renter

// TODO: Shift all arrays to lists so that we aren't abusing memory so much.

import (
	"sync"
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
	ucdq.mu.Lock()
	defer ucdq.mu.Unlock()

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

	// Check if there is a thread running to process the queue.
	if !ucdq.processThreadRunning {
		go ucdq.threadedProcessQueue()
	}
}

// threadedProcessQueue serializes the processing of chunks in the distribution
// queue. If there are priority chunks, it'll handle those first,and then if
// there are no chunks in the priority lane it'll handle things in the non
// priority lane. Each lane is treated like a FIFO.
//
// When de-queuing, if the priority lane is empty and chunks are being pulled
// out of the non-priority lane, the priority buildup can be reset because there
// is no starvation happening of low priority jobs.
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
			ucdq.lowPriorityLane = ucdq.lowPriorityLane[1:]
			ucdq.priorityBuildup = 0
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
	// Give the chunk to each worker, marking the number of workers that have
	// received the chunk. Filter through the workers, ignoring any that are not
	// good for upload, and ignoring any that are on upload cooldown.
	workers := r.staticWorkerPool.callWorkers()
	uc.managedIncreaseRemainingWorkers(len(workers))
	jobsDistributed := 0
	for _, w := range workers {
		if w.callQueueUploadChunk(uc) {
			jobsDistributed++
		}
	}
	close(uc.staticWorkDistributedChan)

	uc.managedUpdateDistributionTime()
	r.repairLog.Printf("Distributed chunk %v of %s to %v workers.", uc.staticIndex, uc.staticSiaPath, jobsDistributed)
	r.managedCleanUpUploadChunk(uc)
}
