package renter

// The download heap is a heap that contains all the chunks that we are trying
// to download, sorted by download priority. Each time there are resources
// available to kick off another download, a chunk is popped off the heap,
// prepared for downloading, and then sent off to the workers.
//
// Download jobs are added to the heap via a function call.

// TODO: renter.threadedDownloadLoop will not need to call `callUpdate` once the
// contractor is reporting changes in the contract set back to the worker
// subsystem.

import (
	"container/heap"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/errors"
)

var (
	errDownloadRenterClosed = errors.New("download could not be scheduled because renter is shutting down")
	errInsufficientHosts    = errors.New("insufficient hosts to recover file")
	errInsufficientPieces   = errors.New("couldn't fetch enough pieces to recover data")
	errPrevErr              = errors.New("download could not be completed due to a previous error")
)

// downloadChunkHeap is a heap that is sorted first by file priority, then by
// the start time of the download, and finally by the index of the chunk.  As
// downloads are queued, they are added to the downloadChunkHeap. As resources
// become available to execute downloads, chunks are pulled off of the heap and
// distributed to workers.
type downloadChunkHeap []*unfinishedDownloadChunk

// Implementation of heap.Interface for downloadChunkHeap.
func (dch downloadChunkHeap) Len() int { return len(dch) }
func (dch downloadChunkHeap) Less(i, j int) bool {
	// First sort by priority.
	if dch[i].staticPriority != dch[j].staticPriority {
		return dch[i].staticPriority > dch[j].staticPriority
	}
	// For equal priority, sort by start time.
	if dch[i].download.staticStartTime != dch[j].download.staticStartTime {
		return dch[i].download.staticStartTime.Before(dch[j].download.staticStartTime)
	}
	// For equal start time (typically meaning it's the same file), sort by
	// chunkIndex.
	//
	// NOTE: To prevent deadlocks when acquiring memory and using writers that
	// will streamline / order different chunks, we must make sure that we sort
	// by chunkIndex such that the earlier chunks are selected first from the
	// heap.
	return dch[i].staticChunkIndex < dch[j].staticChunkIndex
}
func (dch downloadChunkHeap) Swap(i, j int)       { dch[i], dch[j] = dch[j], dch[i] }
func (dch *downloadChunkHeap) Push(x interface{}) { *dch = append(*dch, x.(*unfinishedDownloadChunk)) }
func (dch *downloadChunkHeap) Pop() interface{} {
	old := *dch
	n := len(old)
	x := old[n-1]
	*dch = old[0 : n-1]
	return x
}

// acquireMemoryForDownloadChunk will block until memory is available for the
// chunk to be downloaded. 'false' will be returned if the renter shuts down
// before memory can be acquired.
func (r *Renter) managedAcquireMemoryForDownloadChunk(udc *unfinishedDownloadChunk) bool {
	// The amount of memory required is equal minimum number of pieces plus the
	// overdrive amount.
	//
	// TODO: This allocation assumes that the erasure coding does not need extra
	// memory to decode a bunch of pieces. Optimized erasure coding will not
	// need extra memory to decode a bunch of pieces, though I do not believe
	// our erasure coding has been optimized around this yet, so we may actually
	// go over the memory limits when we decode pieces.
	memoryRequired := uint64(udc.staticOverdrive+udc.erasureCode.MinPieces()) * udc.staticPieceSize
	udc.memoryAllocated = memoryRequired
	return r.memoryManager.Request(memoryRequired, memoryPriorityHigh)
}

// managedAddChunkToDownloadHeap will add a chunk to the download heap in a
// thread-safe way.
func (r *Renter) managedAddChunkToDownloadHeap(udc *unfinishedDownloadChunk) {
	// The purpose of the chunk heap is to block work from happening until there
	// is enough memory available to send off the work. If the chunk does not
	// need any memory to be allocated, it should be given to the workers
	// directly and immediately. This is actually a requirement in our memory
	// model. If a download chunk does not need memory, that means that the
	// memory has already been allocated and will actually be blocking new
	// memory from being allocated until the download is complete. If the job is
	// put in the heap and ends up behind a job which get stuck allocating
	// memory, you get a deadlock.
	//
	// This is functionally equivalent to putting the chunk in the heap with
	// maximum priority, such that the chunk is immediately removed from the
	// heap and distributed to workers - the sole purpose of the heap is to
	// block workers from receiving a chunk until memory has been allocated.
	if !udc.staticNeedsMemory {
		r.managedDistributeDownloadChunkToWorkers(udc)
		return
	}

	// Put the chunk into the chunk heap.
	r.downloadHeapMu.Lock()
	r.downloadHeap.Push(udc)
	r.downloadHeapMu.Unlock()
}

// managedBlockUntilOnline will block until the renter is online. The renter
// will appropriately handle incoming download requests and stop signals while
// waiting.
func (r *Renter) managedBlockUntilOnline() bool {
	for !r.g.Online() {
		select {
		case <-r.tg.StopChan():
			return false
		case <-time.After(offlineCheckFrequency):
		}
	}
	return true
}

// managedDistributeDownloadChunkToWorkers will take a chunk and pass it out to
// all of the workers.
func (r *Renter) managedDistributeDownloadChunkToWorkers(udc *unfinishedDownloadChunk) {
	// Distribute the chunk to workers, marking the number of workers
	// that have received the work.
	r.staticWorkerPool.mu.RLock()
	udc.mu.Lock()
	udc.workersRemaining = len(r.staticWorkerPool.workers)
	udc.mu.Unlock()
	for _, worker := range r.staticWorkerPool.workers {
		worker.callQueueDownloadChunk(udc)
	}
	r.staticWorkerPool.mu.RUnlock()

	// If there are no workers, there will be no workers to attempt to clean up
	// the chunk, so we must make sure that managedCleanUp is called at least
	// once on the chunk.
	udc.managedCleanUp()
}

// managedNextDownloadChunk will fetch the next chunk from the download heap. If
// the download heap is empty, 'nil' will be returned.
func (r *Renter) managedNextDownloadChunk() *unfinishedDownloadChunk {
	r.downloadHeapMu.Lock()
	defer r.downloadHeapMu.Unlock()

	for {
		if r.downloadHeap.Len() <= 0 {
			return nil
		}
		nextChunk := heap.Pop(r.downloadHeap).(*unfinishedDownloadChunk)
		if !nextChunk.download.staticComplete() {
			return nextChunk
		}
	}
}

// managedTryServeChunkFromDisk will try to fetch the chunk from disk if
// possible.
// NOTE: we can only confirm that the file has the right name and size but not
// the it wasn't modified on disk without us knowing. If that's the case then we
// are going to return the updated file to the user which might not be the same
// data that was uploaded.
func (r *Renter) managedTryServeChunkFromDisk(chunk *unfinishedDownloadChunk) bool {
	// Get path at which we expect to find the file.
	localPath := chunk.renterFile.LocalPath()
	if localPath == "" {
		return false
	}
	// Open the file.
	file, err := os.Open(localPath)
	if err != nil {
		r.log.Debugln(err)
		return false
	}
	defer file.Close()
	// If the file on disk doesn't have the right size we don't use it
	// for repairing. This protects in case the user creates a different
	// file at the same location.
	fi, err := file.Stat()
	if err == nil && uint64(fi.Size()) != chunk.renterFile.Size() {
		err = fmt.Errorf("failed to repair from disk due to filesize not matching %v != %v",
			fi.Size(), chunk.renterFile.Size())
		r.log.Debugln(err)
		return false
	}
	// Fetch the chunk from disk.
	sr := io.NewSectionReader(file, int64(chunk.staticChunkIndex*chunk.staticChunkSize), int64(chunk.staticChunkSize))
	pieces, _, err := readDataPieces(sr, chunk.renterFile.ErasureCode(), chunk.renterFile.PieceSize())
	if err != nil {
		r.log.Debugln("failed to readDataPieces to load chunk from disk", err)
		return false
	}
	shards, err := chunk.renterFile.ErasureCode().EncodeShards(pieces)
	if err != nil {
		r.log.Debugln(err)
		return false
	}
	err = chunk.destination.WritePieces(chunk.renterFile.ErasureCode(), shards, chunk.staticFetchOffset, chunk.staticWriteOffset, chunk.staticFetchLength)
	if err != nil {
		r.log.Debugln(err)
		return false
	}
	// Return all the memory.
	chunk.recoveryComplete = true
	chunk.returnMemory()
	// Update the download and signal completion of this chunk.
	chunk.download.mu.Lock()
	defer chunk.download.mu.Unlock()
	chunk.download.chunksRemaining--
	atomic.AddUint64(&chunk.download.atomicDataReceived, chunk.staticFetchLength)
	if chunk.download.chunksRemaining == 0 {
		// Download is complete, send out a notification.
		chunk.download.markComplete()
	}
	return true
}

// threadedDownloadLoop utilizes the worker pool to make progress on any queued
// downloads.
func (r *Renter) threadedDownloadLoop() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// Infinite loop to process downloads. Will return if r.tg.Stop() is called.
LOOP:
	for {
		// Wait until the renter is online.
		if !r.managedBlockUntilOnline() {
			// The renter shut down before the internet connection was restored.
			return
		}

		// Update the worker pool and fetch the current time. The loop will
		// reset after a certain amount of time has passed.
		r.staticWorkerPool.callUpdate()
		workerUpdateTime := time.Now()

		// Pull downloads out of the heap. Will break if the heap is empty, and
		// will reset to the top of the outer loop if a reset condition is met.
		for {
			// Check that we still have an internet connection, and also that we
			// do not need to update the worker pool yet.
			if !r.g.Online() || time.Now().After(workerUpdateTime.Add(workerPoolUpdateTimeout)) {
				// Reset to the top of the outer loop. Either we need to wait
				// until we are online, or we need to refresh the worker pool.
				// The outer loop will handle both situations.
				continue LOOP
			}

			// Get the next chunk.
			nextChunk := r.managedNextDownloadChunk()
			if nextChunk == nil {
				// Break out of the inner loop and wait for more work.
				break
			}

			// Get the required memory to download this chunk.
			if !r.managedAcquireMemoryForDownloadChunk(nextChunk) {
				// The renter shut down before memory could be acquired.
				return
			}
			// Check if we can serve the chunk from disk.
			if !nextChunk.staticDisableDiskFetch && r.managedTryServeChunkFromDisk(nextChunk) {
				continue
			} else if !nextChunk.staticDisableDiskFetch && nextChunk.renterFile.LocalPath() != "" {
				// If the local path is set and we still weren't able to load
				// the chunk from disk, set the localpath to "" for safety.
				entry, err := r.staticFileSet.Open(nextChunk.renterFile.SiaPath())
				if err == nil {
					err = entry.SetLocalPath("")
					entry.Close()
				}
				if err != nil {
					r.log.Debugln("localpath is set but couldn't serve file from disk", err)
				}
			}
			// Disrupt for tests which expect the download to be served from disk.
			println("here we go")
			if r.deps.Disrupt("ForceServeDownloadFromDisk") {
				nextChunk.download.managedFail(errors.New("expected download to be served from disk"))
				continue
			}
			// Distribute the chunk to workers.
			r.managedDistributeDownloadChunkToWorkers(nextChunk)
		}

		// Wait for more work.
		select {
		case <-r.tg.StopChan():
			return
		case <-r.newDownloads:
		}
	}
}
