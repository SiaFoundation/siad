package renter

import (
	"fmt"
	"io"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// uploadChunkID is a unique identifier for each chunk in the renter.
type uploadChunkID struct {
	fileUID siafile.SiafileUID // Unique to each file.
	index   uint64             // Unique to each chunk within a file.
}

// unfinishedUploadChunk contains a chunk from the filesystem that has not
// finished uploading, including knowledge of the progress.
type unfinishedUploadChunk struct {
	// Information about the file. localPath may be the empty string if the file
	// is known not to exist locally.
	id        uploadChunkID
	fileEntry *siafile.SiaFileSetEntry
	threadUID int

	// Information about the chunk, namely where it exists within the file.
	//
	// TODO / NOTE: As we change the file mapper, we're probably going to have
	// to update these fields. Compatibility shouldn't be an issue because this
	// struct is not persisted anywhere, it's always built from other
	// structures.
	fileRecentlySuccessful bool // indicates if the file the chunk is from had a recent successful repair
	health                 float64
	index                  uint64
	length                 uint64
	memoryNeeded           uint64 // memory needed in bytes
	memoryReleased         uint64 // memory that has been returned of memoryNeeded
	minimumPieces          int    // number of pieces required to recover the file.
	offset                 int64  // Offset of the chunk within the file.
	piecesNeeded           int    // number of pieces to achieve a 100% complete upload
	stuck                  bool   // indicates if the chunk was marked as stuck during last repair
	stuckRepair            bool   // indicates if the chunk was identified for repair by the stuck loop
	priority               bool   // indicates if the chunks is supposed to be repaired asap

	// Cache the siapath of the underlying file.
	staticSiaPath string

	// The logical data is the data that is presented to the user when the user
	// requests the chunk. The physical data is all of the pieces that get
	// stored across the network.
	logicalChunkData  [][]byte
	physicalChunkData [][]byte

	// sourceReader is an optional source for the logical chunk data. If
	// available it will be tried before the repair path or remote repair.
	sourceReader io.ReadCloser

	// Worker synchronization fields. The mutex only protects these fields.
	//
	// When a worker passes over a piece for upload to go on standby:
	//	+ the worker should add itself to the list of standby chunks
	//  + the worker should call for memory to be released
	//
	// When a worker passes over a piece because it's not useful:
	//	+ the worker should decrement the number of workers remaining
	//	+ the worker should call for memory to be released
	//
	// When a worker accepts a piece for upload:
	//	+ the worker should increment the number of pieces registered
	// 	+ the worker should mark the piece usage for the piece it is uploading
	//	+ the worker should decrement the number of workers remaining
	//
	// When a worker completes an upload (success or failure):
	//	+ the worker should decrement the number of pieces registered
	//  + the worker should call for memory to be released
	//
	// When a worker completes an upload (failure):
	//	+ the worker should unmark the piece usage for the piece it registered
	//	+ the worker should notify the standby workers of a new available piece
	//
	// When a worker completes an upload successfully:
	//	+ the worker should increment the number of pieces completed
	//	+ the worker should decrement the number of pieces registered
	//	+ the worker should release the memory for the completed piece
	mu               sync.Mutex
	pieceUsage       []bool              // 'true' if a piece is either uploaded, or a worker is attempting to upload that piece.
	piecesCompleted  int                 // number of pieces that have been fully uploaded.
	piecesRegistered int                 // number of pieces that are being uploaded, but aren't finished yet (may fail).
	released         bool                // whether this chunk has been released from the active chunks set.
	unusedHosts      map[string]struct{} // hosts that aren't yet storing any pieces or performing any work.
	workersRemaining int                 // number of inactive workers still able to upload a piece.
	workersStandby   []*worker           // workers that can be used if other workers fail.

	cancelMU sync.Mutex     // cancelMU needs to be held when adding to cancelWG and reading/writing canceled.
	canceled bool           // cancel the work on this chunk.
	cancelWG sync.WaitGroup // WaitGroup to wait on after canceling the uploadchunk.
}

// managedNotifyStandbyWorkers is called when a worker fails to upload a piece, meaning
// that the standby workers may now be needed to help the piece finish
// uploading.
func (uc *unfinishedUploadChunk) managedNotifyStandbyWorkers() {
	// Copy the standby workers into a new slice and reset it since we can't
	// hold the lock while calling the managed function.
	uc.mu.Lock()
	standbyWorkers := make([]*worker, len(uc.workersStandby))
	copy(standbyWorkers, uc.workersStandby)
	uc.workersStandby = uc.workersStandby[:0]
	uc.mu.Unlock()

	for i := 0; i < len(standbyWorkers); i++ {
		standbyWorkers[i].callQueueUploadChunk(uc)
	}
}

// chunkComplete checks some fields of the chunk to determine if the chunk is
// completed. This can either mean that it ran out of workers or that it was
// uploaded successfully.
func (uc *unfinishedUploadChunk) chunkComplete() bool {
	// The whole chunk was uploaded successfully.
	if uc.piecesCompleted == uc.piecesNeeded && uc.piecesRegistered == 0 {
		return true
	}
	// We are no longer doing any uploads and we don't have any workers left.
	if uc.workersRemaining == 0 && uc.piecesRegistered == 0 {
		return true
	}
	return false
}

// readLogicalData initializes the chunk's logicalChunkData using data read from
// r, returning the number of bytes read.
func (uc *unfinishedUploadChunk) readLogicalData(r io.Reader) (uint64, error) {
	// Allocate data pieces and fill them with data from r.
	ec := uc.fileEntry.ErasureCode()
	dataPieces := make([][]byte, ec.MinPieces())
	var total uint64
	for i := range dataPieces {
		dataPieces[i] = make([]byte, uc.fileEntry.PieceSize())
		n, err := io.ReadFull(r, dataPieces[i])
		total += uint64(n)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return total, errors.AddContext(err, "failed to read chunk from source reader")
		}
	}
	// Encode the data pieces, forming the chunk's logical data.
	uc.logicalChunkData, _ = ec.EncodeShards(dataPieces)
	return total, nil
}

// managedDistributeChunkToWorkers will take a chunk with fully prepared
// physical data and distribute it to the worker pool.
func (r *Renter) managedDistributeChunkToWorkers(uc *unfinishedUploadChunk) {
	// Give the chunk to each worker, marking the number of workers that have
	// received the chunk. The workers cannot be interacted with while the
	// renter is holding a lock, so we need to build a list of workers while
	// under lock and then launch work jobs after that.
	r.staticWorkerPool.mu.RLock()
	uc.workersRemaining += len(r.staticWorkerPool.workers)
	workers := make([]*worker, 0, len(r.staticWorkerPool.workers))
	for _, worker := range r.staticWorkerPool.workers {
		workers = append(workers, worker)
	}
	r.staticWorkerPool.mu.RUnlock()
	for _, worker := range workers {
		worker.callQueueUploadChunk(uc)
	}
}

// managedDownloadLogicalChunkData will fetch the logical chunk data by sending a
// download to the renter's downloader, and then using the data that gets
// returned.
func (r *Renter) managedDownloadLogicalChunkData(chunk *unfinishedUploadChunk) error {
	//  Determine what the download length should be. Normally it is just the
	//  chunk size, but if this is the last chunk we need to download less
	//  because the file is not that large.
	//
	// TODO: There is a disparity in the way that the upload and download code
	// handle the last chunk, which may not be full sized.
	downloadLength := chunk.length
	if chunk.index == chunk.fileEntry.NumChunks()-1 && chunk.fileEntry.Size()%chunk.length != 0 {
		downloadLength = chunk.fileEntry.Size() % chunk.length
	}

	// Prepare snapshot.
	snap, err := chunk.fileEntry.Snapshot()
	if err != nil {
		return err
	}
	// Create the download.
	buf := NewDownloadDestinationBuffer()
	d, err := r.managedNewDownload(downloadParams{
		destination:     buf,
		destinationType: "buffer",
		file:            snap,

		latencyTarget: 200e3, // No need to rush latency on repair downloads.
		length:        downloadLength,
		needsMemory:   false, // We already requested memory, the download memory fits inside of that.
		offset:        uint64(chunk.offset),
		overdrive:     0, // No need to rush the latency on repair downloads.
		priority:      0, // Repair downloads are completely de-prioritized.
	})
	if err != nil {
		return err
	}
	// Start the download.
	if err := d.Start(); err != nil {
		return err
	}

	// Register some cleanup for when the download is done.
	d.OnComplete(func(_ error) error {
		// Update the access time when the download is done.
		return chunk.fileEntry.SiaFile.UpdateAccessTime()
	})

	// Wait for the download to complete.
	select {
	case <-d.completeChan:
	case <-r.tg.StopChan():
		return errors.New("repair download interrupted by stop call")
	}
	if d.Err() != nil {
		buf.pieces = nil
		return d.Err()
	}
	chunk.logicalChunkData = buf.pieces
	return nil
}

// threadedFetchAndRepairChunk will fetch the logical data for a chunk, create
// the physical pieces for the chunk, and then distribute them.
func (r *Renter) threadedFetchAndRepairChunk(chunk *unfinishedUploadChunk) {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// Calculate the amount of memory needed for erasure coding. This will need
	// to be released if there's an error before erasure coding is complete.
	erasureCodingMemory := chunk.fileEntry.PieceSize() * uint64(chunk.fileEntry.ErasureCode().MinPieces())

	// Calculate the amount of memory to release due to already completed
	// pieces. This memory gets released during encryption, but needs to be
	// released if there's a failure before encryption happens.
	var pieceCompletedMemory uint64
	for i := 0; i < len(chunk.pieceUsage); i++ {
		if chunk.pieceUsage[i] {
			pieceCompletedMemory += modules.SectorSize
		}
	}

	// Ensure that memory is released and that the chunk is cleaned up properly
	// after the chunk is distributed.
	//
	// Need to ensure the erasure coding memory is released as well as the
	// physical chunk memory. Physical chunk memory is released by setting
	// 'workersRemaining' to zero if the repair fails before being distributed
	// to workers. Erasure coding memory is released manually if the repair
	// fails before the erasure coding occurs.
	defer r.managedCleanUpUploadChunk(chunk)

	// Fetch the logical data for the chunk.
	err = r.managedFetchLogicalChunkData(chunk)
	if err != nil {
		// Logical data is not available, cannot upload. Chunk will not be
		// distributed to workers, therefore set workersRemaining equal to zero.
		// The erasure coding memory has not been released yet, be sure to
		// release that as well.
		chunk.logicalChunkData = nil
		chunk.workersRemaining = 0
		r.memoryManager.Return(erasureCodingMemory + pieceCompletedMemory)
		chunk.memoryReleased += erasureCodingMemory + pieceCompletedMemory
		r.repairLog.Printf("Unable to fetch the logical data for chunk %v of %s - marking as stuck: %v", chunk.index, chunk.staticSiaPath, err)

		// Mark chunk as stuck
		err = chunk.fileEntry.SetStuck(chunk.index, true)
		if err != nil {
			r.repairLog.Printf("Error marking chunk %v of file %s as stuck: %v", chunk.index, chunk.staticSiaPath, err)
		}
		return
	}

	// Create the physical pieces for the data. Immediately release the logical
	// data.
	//
	// TODO: The logical data is the first few chunks of the physical data. If
	// the memory is not being handled cleanly here, we should leverage that
	// fact to reduce the total memory required to create the physical data.
	// That will also change the amount of memory we need to allocate, and the
	// number of times we need to return memory.
	err = chunk.fileEntry.ErasureCode().Reconstruct(chunk.logicalChunkData)
	chunk.physicalChunkData = chunk.logicalChunkData
	chunk.logicalChunkData = nil
	r.memoryManager.Return(erasureCodingMemory)
	chunk.memoryReleased += erasureCodingMemory
	if err != nil {
		// Physical data is not available, cannot upload. Chunk will not be
		// distributed to workers, therefore set workersRemaining equal to zero.
		chunk.workersRemaining = 0
		r.memoryManager.Return(pieceCompletedMemory)
		chunk.memoryReleased += pieceCompletedMemory
		for i := 0; i < len(chunk.physicalChunkData); i++ {
			chunk.physicalChunkData[i] = nil
		}
		r.repairLog.Printf("Fetching physical data of chunk %v from %s as stuck: %v", chunk.index, chunk.staticSiaPath, err)

		// Mark chunk as stuck
		err = chunk.fileEntry.SetStuck(chunk.index, true)
		if err != nil {
			r.repairLog.Printf("Error marking chunk %v of file %s as stuck: %v", chunk.index, chunk.staticSiaPath, err)
		}
		return
	}

	// Sanity check - we should have at least as many physical data pieces as we
	// do elements in our piece usage.
	if len(chunk.physicalChunkData) < len(chunk.pieceUsage) {
		r.log.Critical("not enough physical pieces to match the upload settings of the file")
		// Mark chunk as stuck
		r.repairLog.Printf("Marking chunk %v of %s as stuck due to insufficient physical pieces", chunk.index, chunk.staticSiaPath)
		err = chunk.fileEntry.SetStuck(chunk.index, true)
		if err != nil {
			r.repairLog.Printf("Error marking chunk %v of file %s as stuck: %v", chunk.index, chunk.staticSiaPath, err)
		}
		return
	}
	// Loop through the pieces and encrypt any that are needed, while dropping
	// any pieces that are not needed.
	for i := 0; i < len(chunk.pieceUsage); i++ {
		if chunk.pieceUsage[i] {
			chunk.physicalChunkData[i] = nil
		} else {
			// Encrypt the piece.
			key := chunk.fileEntry.MasterKey().Derive(chunk.index, uint64(i))
			chunk.physicalChunkData[i] = key.EncryptBytes(chunk.physicalChunkData[i])
			// If the piece was not a full sector, pad it accordingly with random bytes.
			if short := int(modules.SectorSize) - len(chunk.physicalChunkData[i]); short > 0 {
				// The form `append(obj, make([]T, n))` will be optimized by the
				// compiler to eliminate unneeded allocations starting go 1.11.
				chunk.physicalChunkData[i] = append(chunk.physicalChunkData[i], make([]byte, short)...)
				fastrand.Read(chunk.physicalChunkData[i][len(chunk.physicalChunkData[i])-short:])
			}
		}
	}
	// Return the released memory.
	if pieceCompletedMemory > 0 {
		r.memoryManager.Return(pieceCompletedMemory)
		chunk.memoryReleased += pieceCompletedMemory
	}

	// Distribute the chunk to the workers.
	r.managedDistributeChunkToWorkers(chunk)
}

// managedFetchLogicalChunkData will get the raw data for a chunk, pulling it from disk if
// possible but otherwise queueing a download.
//
// chunk.data should be passed as 'nil' to the download, to keep memory usage as
// light as possible.
func (r *Renter) managedFetchLogicalChunkData(chunk *unfinishedUploadChunk) error {
	// If a sourceReader is available, use it.
	if chunk.sourceReader != nil {
		defer chunk.sourceReader.Close()
		n, err := chunk.readLogicalData(chunk.sourceReader)
		if err != nil {
			return err
		}
		// Adjust the filesize. Since we don't know the length of the stream
		// beforehand we simply assume that a whole chunk will be added to the
		// file. That's why we subtract the difference between the size of a
		// chunk and n here.
		adjustedSize := chunk.fileEntry.Size() - chunk.length + n
		if errSize := chunk.fileEntry.SetFileSize(adjustedSize); errSize != nil {
			return errors.AddContext(errSize, "failed to adjust FileSize")
		}
		return nil
	}

	// Download the chunk if it's not on disk.
	if chunk.fileEntry.LocalPath() == "" {
		return r.managedDownloadLogicalChunkData(chunk)
	}

	// Try to read the data from disk. If that fails, fallback to downloading.
	err := func() error {
		osFile, err := os.Open(chunk.fileEntry.LocalPath())
		if err != nil {
			return err
		}
		defer osFile.Close()
		sr := io.NewSectionReader(osFile, chunk.offset, int64(chunk.length))
		_, err = chunk.readLogicalData(sr)
		return err
	}()
	if err != nil {
		r.log.Debugln("failed to read file, downloading instead:", err)
		return r.managedDownloadLogicalChunkData(chunk)
	}
	return nil
}

// managedCleanUpUploadChunk will check the state of the chunk and perform any
// cleanup required. This can include returning rememory and releasing the chunk
// from the map of active chunks in the chunk heap.
func (r *Renter) managedCleanUpUploadChunk(uc *unfinishedUploadChunk) {
	uc.mu.Lock()
	piecesAvailable := 0
	var memoryReleased uint64
	// Release any unnecessary pieces, counting any pieces that are
	// currently available.
	for i := 0; i < len(uc.pieceUsage); i++ {
		// Skip the piece if it's not available.
		if uc.pieceUsage[i] {
			continue
		}

		// If we have all the available pieces we need, release this piece.
		// Otherwise, mark that there's another piece available. This algorithm
		// will prefer releasing later pieces, which improves computational
		// complexity for erasure coding.
		if piecesAvailable >= uc.workersRemaining {
			memoryReleased += modules.SectorSize
			if len(uc.physicalChunkData) < len(uc.pieceUsage) {
				// TODO handle this. Might happen if erasure coding the chunk failed.
			}
			uc.physicalChunkData[i] = nil
			// Mark this piece as taken so that we don't double release memory.
			uc.pieceUsage[i] = true
		} else {
			piecesAvailable++
		}
	}

	// Check if the chunk needs to be removed from the list of active
	// chunks. It needs to be removed if the chunk is complete, but hasn't
	// yet been released.
	chunkComplete := uc.chunkComplete()
	released := uc.released
	if chunkComplete && !released {
		if uc.piecesCompleted >= uc.piecesNeeded {
			r.repairLog.Printf("Completed repair for chunk %v of %s, %v pieces were completed out of %v", uc.index, uc.staticSiaPath, uc.piecesCompleted, uc.piecesNeeded)
		} else {
			r.repairLog.Printf("Repair of chunk %v of %s was unsuccessful, %v pieces were completed out of %v", uc.index, uc.staticSiaPath, uc.piecesCompleted, uc.piecesNeeded)
		}
		uc.released = true
	}
	uc.memoryReleased += uint64(memoryReleased)
	totalMemoryReleased := uc.memoryReleased
	uc.mu.Unlock()

	// If there are pieces available, add the standby workers to collect them.
	// Standby workers are only added to the chunk when piecesAvailable is equal
	// to zero, meaning this code will only trigger if the number of pieces
	// available increases from zero. That can only happen if a worker
	// experiences an error during upload.
	if piecesAvailable > 0 {
		uc.managedNotifyStandbyWorkers()
	}
	// If required, remove the chunk from the set of repairing chunks.
	if chunkComplete && !released {
		r.managedUpdateUploadChunkStuckStatus(uc)
		// Close the file entry unless disrupted.
		if !r.deps.Disrupt("disableCloseUploadEntry") {
			err := uc.fileEntry.Close()
			if err != nil {
				r.repairLog.Printf("WARN: file not closed after chunk upload complete: %v %v", r.staticFileSet.SiaPath(uc.fileEntry), err)
			}
		}
		// Remove the chunk from the repairingChunks map
		r.uploadHeap.managedMarkRepairDone(uc.id)
		// Signal garbage collector to free memory before returning it to the manager.
		uc.logicalChunkData = nil
		uc.physicalChunkData = nil
	}
	// If required, return the memory to the renter.
	if memoryReleased > 0 {
		r.memoryManager.Return(memoryReleased)
	}
	// Sanity check - all memory should be released if the chunk is complete.
	if chunkComplete && totalMemoryReleased != uc.memoryNeeded {
		r.log.Critical("No workers remaining, but not all memory released:", uc.workersRemaining, uc.piecesRegistered, uc.memoryReleased, uc.memoryNeeded)
	}
}

// managedSetStuckAndClose sets the unfinishedUploadChunk's stuck status,
// triggers threadedBubble to update the directory, and then closes the
// fileEntry
func (r *Renter) managedSetStuckAndClose(uc *unfinishedUploadChunk, stuck bool) error {
	// Update chunk stuck status
	err := uc.fileEntry.SetStuck(uc.index, stuck)
	if err != nil {
		return fmt.Errorf("WARN: unable to update chunk stuck status for file %v: %v", r.staticFileSet.SiaPath(uc.fileEntry), err)
	}
	// Close SiaFile
	err = uc.fileEntry.Close()
	if err != nil {
		return fmt.Errorf("WARN: unable to close siafile %v", r.staticFileSet.SiaPath(uc.fileEntry))
	}
	// Signal garbage collector to free memory.
	uc.physicalChunkData = nil
	uc.logicalChunkData = nil
	return nil
}

// managedUpdateUploadChunkStuckStatus checks to see if the repair was
// successful and then updates the chunk's stuck status
func (r *Renter) managedUpdateUploadChunkStuckStatus(uc *unfinishedUploadChunk) {
	// Grab necessary information from upload chunk under lock
	uc.mu.Lock()
	index := uc.id.index
	stuck := uc.stuck
	minimumPieces := uc.minimumPieces
	piecesCompleted := uc.piecesCompleted
	piecesNeeded := uc.piecesNeeded
	stuckRepair := uc.stuckRepair
	uc.mu.Unlock()

	// Determine if repair was successful.
	successfulRepair := float64(piecesNeeded-piecesCompleted)/float64(piecesNeeded-minimumPieces) < RepairThreshold

	// Check if renter is shutting down
	var renterError bool
	select {
	case <-r.tg.StopChan():
		renterError = true
	default:
		// Check that the renter is still online
		if !r.g.Online() {
			renterError = true
		}
	}

	// If the repair was unsuccessful and there was a renter error then return
	if !successfulRepair && renterError {
		r.repairLog.Debugln("WARN: repair unsuccessful for chunk", uc.id, "due to an error with the renter")
		return
	}
	// Log if the repair was unsuccessful
	if !successfulRepair {
		r.repairLog.Debugln("WARN: repair unsuccessful, marking chunk", uc.id, "as stuck", float64(piecesCompleted)/float64(piecesNeeded))
	} else {
		r.repairLog.Debugln("SUCCESS: repair successful, marking chunk as non-stuck:", uc.id)
	}
	// Update chunk stuck status
	if err := uc.fileEntry.SetStuck(index, !successfulRepair); err != nil {
		r.repairLog.Printf("WARN: could not set chunk %v stuck status for file %v: %v", uc.id, uc.fileEntry.SiaFilePath(), err)
	}

	// Check to see if the chunk was stuck and now is successfully repaired by
	// the stuck loop
	if stuck && successfulRepair && stuckRepair {
		r.repairLog.Debugln("Stuck chunk", uc.id, "successfully repaired")
		// Add file to the successful stuck repair stack if there are still
		// stuck chunks to repair
		if uc.fileEntry.NumStuckChunks() > 0 {
			r.stuckStack.managedPush(r.staticFileSet.SiaPath(uc.fileEntry))
		}
		// Signal the stuck loop that the chunk was successfully repaired
		select {
		case <-r.tg.StopChan():
			r.repairLog.Debugln("WARN: renter shut down before the stuck loop was signalled that the stuck repair was successful")
			return
		case r.uploadHeap.stuckChunkSuccess <- struct{}{}:
		default:
		}
	}
}
