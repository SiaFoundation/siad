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
	"io"
	"os"
	"sync/atomic"
	"time"
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
	// immediately.
	//
	// When a chunk does not need memory, that is most commonly because the
	// repair loop has already allocated memory for the download and is waiting
	// until the download returns before it can release the memory. If the chunk
	// goes into the heap and waits until other chunks are selected, that memory
	// will not be released until other downloads have been processed. If those
	// other downloads need memory and this chunk has consumed the last
	// remaining memory, you get a deadlock.
	if !udc.staticNeedsMemory {
		// If fetching the file from disk is disabled, the chunk will be
		// immediately distributed to the workers. If fetching from disk is not
		// disabled, there will be an attempt to fetch the data from disk, and
		// the work will only be distributed for downloading if the disk fetch
		// fails.
		if udc.staticDisableDiskFetch || !r.managedTryFetchChunkFromDisk(udc) {
			r.managedDistributeDownloadChunkToWorkers(udc)
		}
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

// managedTryFetchChunkFromDisk will try to fetch the chunk from disk if
// possible.
//
// NOTE: Unable to do an integrity check on the data if fetched locally because
// we only have checksum information on entire chunks, while the user may only
// be requesting a small piece. It seems a bit aggressive to perform an
// integrity check on an entire chunk if there is a user requesting just 1 kb of
// data. Such a check could also be a DoS vector for entities serving many
// users, such as viewnodes.
//
// NOTE: If there is a failure, we could wipe the local path of the file, since
// it no longer matches. The challenge of doing that is that the download chunk
// only has a snapshot instead of the actual file, meaning we can't be certain
// when we clear the local path that we are actually clearing the right object -
// there could be a rename or similar event. To clear the local path, the
// download object would have to retain the fileNode as well as the snapshot.
// Snapshot needs to be retained because the download will fetch the file as it
// was when the download as issued, ignoring any updates or modifications to the
// file that happen throughout the download.
func (r *Renter) managedTryFetchChunkFromDisk(chunk *unfinishedDownloadChunk) bool {
	// Get path at which we expect to find the file.
	fileName := chunk.renterFile.SiaPath().Name()
	localPath := chunk.renterFile.LocalPath()
	if localPath == "" {
		return false
	}
	// Open the file.
	file, err := os.Open(localPath)
	if err != nil {
		r.log.Debugf("managedTryFetchChunkFromDisk failed to open file %v for %v: %v", localPath, fileName, err)
		return false
	}

	// An entire integrity check can't performed, however we can at least check
	// that the filesize is the same.
	fi, err := file.Stat()
	if err != nil {
		r.log.Printf("local file %v of file %v was not used for download because the statistics could not be fetched: %v", localPath, fileName, err)
		return false
	}
	fiSize := uint64(fi.Size())
	rfSize := chunk.renterFile.Size()
	if fiSize != rfSize {
		r.log.Printf("local file %v of file %v was not used for download: filesizes are mismatched: %v vs %v", localPath, fileName, fiSize, rfSize)
		return false
	}

	// Fetch the chunk from disk.
	if err := r.tg.Add(); err != nil {
		return false
	}
	go func() (success bool) {
		defer r.tg.Done()
		defer file.Close()
		// Try downloading if serving from disk failed.
		defer func() {
			if success {
				// Return the memory for the chunk on success and finalize the
				// recovery.
				atomic.AddUint64(&chunk.download.atomicDataReceived, chunk.staticFetchLength)
				atomic.AddUint64(&chunk.download.atomicTotalDataTransferred, chunk.staticFetchLength)
				chunk.managedFinalizeRecovery()
				chunk.returnMemory()
			} else {
				// If it failed, download it instead.
				r.managedDistributeDownloadChunkToWorkers(chunk)
			}
		}()
		// Check if download was already aborted.
		select {
		case <-chunk.download.completeChan:
			return false
		default:
		}
		sr := io.NewSectionReader(file, int64(chunk.staticChunkIndex*chunk.staticChunkSize), int64(chunk.staticChunkSize))
		pieces, _, err := readDataPieces(sr, chunk.renterFile.ErasureCode(), chunk.renterFile.PieceSize())
		if err != nil {
			r.log.Debugf("managedTryFetchChunkFromDisk failed to read data pieces from %v for %v: %v\n",
				localPath, fileName, err)
			return false
		}
		shards, err := chunk.renterFile.ErasureCode().EncodeShards(pieces)
		if err != nil {
			r.log.Debugf("managedTryFetchChunkFromDisk failed to encode data pieces from %v for %v: %v",
				localPath, fileName, err)
			return false
		}
		err = chunk.destination.WritePieces(chunk.renterFile.ErasureCode(), shards, chunk.staticFetchOffset, chunk.staticWriteOffset, chunk.staticFetchLength)
		if err != nil {
			r.log.Debugf("managedTryFetchChunkFromDisk failed to write data pieces from %v for %v: %v",
				localPath, fileName, err)
			return false
		}
		return true
	}()
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
			if !nextChunk.staticDisableDiskFetch && r.managedTryFetchChunkFromDisk(nextChunk) {
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
