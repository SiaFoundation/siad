package renter

import (
	"fmt"
	"io"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
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
	fileEntry *filesystem.FileNode

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

	// staticExpectedPieceRoots is a list of piece roots that are known for the
	// chunk. If the roots are blank, it means there is no expectation for the
	// root. This field is used to prevent file corruption when repairing from
	// an authenticated source. For example, if repairing from a local file,
	// it's possible that the local file has changed since being originally
	// uploaded. This field allows us to check after we load the file locally
	// and be confident that the data now is the same as what it used to be.
	staticExpectedPieceRoots []crypto.Hash

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
	availableChan    chan struct{} // used to signal to other processes that the chunk is available on the Sia network. Error needs to be checked.
	err              error
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

// staticAvailable returns whether or not the chunk is available yet on the Sia
// network.
func (uc *unfinishedUploadChunk) staticAvailable() bool {
	select {
	case <-uc.availableChan:
		return true
	default:
		return false
	}
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

// readDataPieces reads dataPieces from a io.Reader and stores them in a
// [][]byte ready to be encoded using an ErasureCoder.
func readDataPieces(r io.Reader, ec modules.ErasureCoder, pieceSize uint64) ([][]byte, uint64, error) {
	dataPieces := make([][]byte, ec.MinPieces())
	var total uint64
	for i := range dataPieces {
		dataPieces[i] = make([]byte, pieceSize)
		n, err := io.ReadFull(r, dataPieces[i])
		total += uint64(n)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return nil, 0, errors.AddContext(err, "failed to read chunk from source reader")
		}
	}
	return dataPieces, total, nil
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

// padAndEncryptPiece will add padding to a piece and then encrypt it.
func (uuc *unfinishedUploadChunk) padAndEncryptPiece(i int) {
	// If the piece is not a full sector, pad it with empty bytes. The padding
	// is done before applying encryption, meaning the data fed to the host does
	// not have a bunch of zeroes in it.
	//
	// This has the extra benefit of making the result deterministic, which is
	// important when checking the integrity of a local file later on.
	short := int(modules.SectorSize) - len(uuc.logicalChunkData[i])
	if short > 0 {
		// The form `append(obj, make([]T, n))` will be optimized by the
		// compiler to eliminate unneeded allocations starting go 1.11.
		uuc.logicalChunkData[i] = append(uuc.logicalChunkData[i], make([]byte, short)...)
	}
	// Encrypt the piece.
	key := uuc.fileEntry.MasterKey().Derive(uuc.index, uint64(i))
	// TODO: Switch this to perform in-place encryption.
	uuc.logicalChunkData[i] = key.EncryptBytes(uuc.logicalChunkData[i])
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
	snap, err := chunk.fileEntry.Snapshot(r.staticFileSystem.FileSiaPath(chunk.fileEntry))
	if err != nil {
		return err
	}
	// Create the download. 'disableLocalFetch' is set to true here to prevent
	// the download from trying to load the chunk from disk. This field is set
	// because the local fetch version of the download call does not perform an
	// integrity check.
	buf := NewDownloadDestinationBuffer()
	d, err := r.managedNewDownload(downloadParams{
		destination:       buf,
		destinationType:   "buffer",
		disableLocalFetch: true,
		file:              snap,

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

	// Reconstruct the pieces.
	//
	// TODO: Ideally there is a way to perform the reconstruction here such that
	// only the necessary pieces are reconstructed.
	err = chunk.fileEntry.ErasureCode().Reconstruct(chunk.logicalChunkData)
	if err != nil {
		return errors.AddContext(err, "unable to reconstruct the data downloaded from the network during repair")
	}
	// Loop through the pieces and encrypt any that are needed, while dropping
	// any pieces that are not needed.
	var wg sync.WaitGroup
	for i := 0; i < len(chunk.pieceUsage); i++ {
		if chunk.pieceUsage[i] {
			chunk.logicalChunkData[i] = nil
			continue
		}
		wg.Add(1)
		go func(i int) {
			chunk.padAndEncryptPiece(i)
			wg.Done()
		}(i)
	}
	wg.Wait()
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

		// If Sia is not currently online, the chunk doesn't need to be marked
		// as stuck.
		if !r.g.Online() {
			return
		}
		// Mark chunk as stuck because the renter was unable to fetch the
		// logical data.
		r.repairLog.Printf("Marking a chunk %v of file %s as stuck because the logical data could not be fetched: %v", chunk.index, chunk.staticSiaPath, err)
		err = chunk.fileEntry.SetStuck(chunk.index, true)
		if err != nil {
			r.repairLog.Printf("Error marking chunk %v of file %s as stuck: %v", chunk.index, chunk.staticSiaPath, err)
		}
		return
	}
	// Return the erasure coding memory. This is not handled by the data
	// fetching, where the erasure coding occurs.
	r.memoryManager.Return(erasureCodingMemory + pieceCompletedMemory)
	chunk.memoryReleased += erasureCodingMemory + pieceCompletedMemory
	// Swap the physical chunk data and the logical chunk data. There is
	// probably no point to having both, given that we perform such a clean
	// handoff here, but since the code is already written this way, it may be
	// best to leave it.
	chunk.physicalChunkData = chunk.logicalChunkData
	chunk.logicalChunkData = nil

	// Sanity check - we should have at least as many physical data pieces as we
	// do elements in our piece usage.
	if len(chunk.physicalChunkData) < len(chunk.pieceUsage) {
		r.log.Critical("not enough physical pieces to match the upload settings of the file")
		return
	}

	// Distribute the chunk to the workers.
	r.managedDistributeChunkToWorkers(chunk)
}

// staticEncryptAndCheckIntegrity will run through the pieces that are
// presented, assumed to be already erasure coded. The integrity check will
// perform the encryption on the pieces and then ensure that the result matches
// any known roots for the renter.
func (uuc *unfinishedUploadChunk) staticEncryptAndCheckIntegrity() error {
	// Verify that all of the shards match the piece roots we are expecting. Use
	// one thread per piece so that the verification is multicore.
	var zeroHash crypto.Hash
	var wg sync.WaitGroup
	failures := make([]bool, len(uuc.logicalChunkData))
	for i := range uuc.logicalChunkData {
		// Skip if there is no data.
		if uuc.logicalChunkData[i] == nil {
			continue
		}
		// Skip if this piece is not needed.
		if uuc.pieceUsage[i] {
			uuc.logicalChunkData[i] = nil
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			// Encrypt and pad the piece with the given index.
			uuc.padAndEncryptPiece(i)

			// Perform the integrity check. Skip the integrity check on this
			// piece if there is no hash available.
			if uuc.staticExpectedPieceRoots[i] == zeroHash {
				return
			}
			root := crypto.MerkleRoot(uuc.logicalChunkData[i])
			if root != uuc.staticExpectedPieceRoots[i] {
				failures[i] = true
			}
		}(i)

	}
	wg.Wait()

	// Scan through and see if there were any failures.
	for _, failure := range failures {
		if failure {
			// Integrity check has failed. Create an error and quit.
			return errors.New("physical data integrity check has failed")
		}
	}
	return nil
}

// staticReadLogicalData initializes the chunk's logicalChunkData using data read from
// r, returning the number of bytes read.
func (uc *unfinishedUploadChunk) staticReadLogicalData(r io.Reader) (uint64, error) {
	// Allocate data pieces and fill them with data from r.
	dataPieces, total, err := readDataPieces(r, uc.fileEntry.ErasureCode(), uc.fileEntry.PieceSize())
	if err != nil {
		return 0, err
	}
	// Encode the data pieces, forming the chunk's logical data.
	//
	// TODO: Ideally there is a way to only encode the shards that we need.
	uc.logicalChunkData, _ = uc.fileEntry.ErasureCode().EncodeShards(dataPieces)
	return total, nil
}

// staticFetchLogicalDataFromReader will load the logical data for a chunk from
// a reader, and perform an integrity check on the chunk to ensure correctness.
func (r *Renter) staticFetchLogicalDataFromReader(uuc *unfinishedUploadChunk) error {
	defer uuc.sourceReader.Close()

	// Grab the logical data from the reader.
	n, err := uuc.staticReadLogicalData(uuc.sourceReader)
	if err != nil {
		return errors.AddContext(err, "unable to read the chunk data from the source reader")
	}

	// Perform an integrity check on the data that was pulled from the reader.
	err = uuc.staticEncryptAndCheckIntegrity()
	if err != nil {
		return errors.AddContext(err, "source data does not match previously uploaded data - blocking corrupt repair")
	}

	// Adjust the filesize. Since we don't know the length of the stream
	// beforehand we simply assume that a whole chunk will be added to the
	// file. That's why we subtract the difference between the size of a
	// chunk and n here.
	adjustedSize := uuc.fileEntry.Size() - uuc.length + n
	if errSize := uuc.fileEntry.SetFileSize(adjustedSize); errSize != nil {
		return errors.AddContext(errSize, "failed to adjust FileSize")
	}
	return nil
}

// managedFetchLogicalChunkData will get the raw data for a chunk, pulling it from disk if
// possible but otherwise queueing a download.
//
// uuc.data should be passed as 'nil' to the download, to keep memory usage as
// light as possible.
func (r *Renter) managedFetchLogicalChunkData(uuc *unfinishedUploadChunk) error {
	// Use a sourceReader if one is available.
	if uuc.sourceReader != nil {
		err := r.staticFetchLogicalDataFromReader(uuc)
		if err != nil {
			// Attempt to fall back to downloading the data from remote.
			r.repairLog.Println("Unable to load logical data from source reader, falling back to remote download:", err)
		} else {
			return nil
		}
	}

	// No source reader available. Check if there's potentially a local file. If
	// there is no local file, fall back to doing a remote repair.
	// disk.
	if uuc.fileEntry.LocalPath() == "" {
		return r.managedDownloadLogicalChunkData(uuc)
	}

	//  Try to fetch the file from the local path and upload there.
	err := func() error {
		osFile, err := os.Open(uuc.fileEntry.LocalPath())
		if os.IsNotExist(err) {
			// The file doesn't exist on disk anymore, drop the local path.
			err = errors.Compose(err, uuc.fileEntry.SetLocalPath(""))
		}
		if err != nil {
			return errors.AddContext(err, "unable to open file locally")
		}
		defer osFile.Close()
		sr := io.NewSectionReader(osFile, uuc.offset, int64(uuc.length))
		dataPieces, _, err := readDataPieces(sr, uuc.fileEntry.ErasureCode(), uuc.fileEntry.PieceSize())
		if err != nil {
			return errors.AddContext(err, "unable to read the data from the local file")
		}
		uuc.logicalChunkData, _ = uuc.fileEntry.ErasureCode().EncodeShards(dataPieces)
		err = uuc.staticEncryptAndCheckIntegrity()
		if err != nil {
			return errors.AddContext(err, "local file failed the integrity check")
		}
		return nil
	}()
	if err != nil {
		r.log.Printf("falling back to remote download for repair: fetch from local file %v failed: %v", uuc.fileEntry.LocalPath(), err)
		return r.managedDownloadLogicalChunkData(uuc)
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
			uc.physicalChunkData[i] = nil
			// Mark this piece as taken so that we don't double release memory.
			uc.pieceUsage[i] = true
		} else {
			piecesAvailable++
		}
	}

	// Check if the chunk is now available.
	if uc.piecesCompleted >= uc.minimumPieces && !uc.staticAvailable() && !uc.released {
		close(uc.availableChan)
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
		if !uc.staticAvailable() {
			uc.err = errors.New("unable to upload file, file is not available on the network")
			close(uc.availableChan)
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
			uc.fileEntry.Close()
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
		return fmt.Errorf("WARN: unable to update chunk stuck status for file %v: %v", uc.fileEntry.SiaFilePath(), err)
	}
	// Close SiaFile
	uc.fileEntry.Close()
	if err != nil {
		return fmt.Errorf("WARN: unable to close siafile %v", uc.fileEntry.SiaFilePath())
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
		r.log.Debugln("WARN: repair unsuccessful for chunk", uc.id, "due to an error with the renter")
		return
	}
	// Log if the repair was unsuccessful
	if !successfulRepair {
		r.log.Debugln("WARN: repair unsuccessful, marking chunk", uc.id, "as stuck", float64(piecesCompleted)/float64(piecesNeeded))
	} else {
		r.log.Debugln("SUCCESS: repair successful, marking chunk as non-stuck:", uc.id)
	}
	// Update chunk stuck status
	if err := uc.fileEntry.SetStuck(index, !successfulRepair); err != nil {
		r.log.Printf("WARN: could not set chunk %v stuck status for file %v: %v", uc.id, uc.fileEntry.SiaFilePath(), err)
	}

	// Check to see if the chunk was stuck and now is successfully repaired by
	// the stuck loop
	if stuck && successfulRepair && stuckRepair {
		r.log.Debugln("Stuck chunk", uc.id, "successfully repaired")
		// Add file to the successful stuck repair stack if there are still
		// stuck chunks to repair
		if uc.fileEntry.NumStuckChunks() > 0 {
			r.stuckStack.managedPush(r.staticFileSystem.FileSiaPath(uc.fileEntry))
		}
		// Signal the stuck loop that the chunk was successfully repaired
		select {
		case <-r.tg.StopChan():
			r.log.Debugln("WARN: renter shut down before the stuck loop was signalled that the stuck repair was successful")
			return
		case r.uploadHeap.stuckChunkSuccess <- struct{}{}:
		default:
		}
	}
}
