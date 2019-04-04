package renter

import (
	"container/heap"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
)

// repairTarget is a helper type for telling the repair heap what type of
// files/chunks to target for repair
type repairTarget int

// targetStuckChunks tells the repair loop to target stuck chunks for repair and
// targetUnstuckChunks tells the repair loop to target unstuck chunks for repair
const (
	targetError repairTarget = iota
	targetStuckChunks
	targetUnstuckChunks
)

// uploadChunkHeap is a bunch of priority-sorted chunks that need to be either
// uploaded or repaired.
type uploadChunkHeap []*unfinishedUploadChunk

// Implementation of heap.Interface for uploadChunkHeap.
func (uch uploadChunkHeap) Len() int { return len(uch) }
func (uch uploadChunkHeap) Less(i, j int) bool {
	// If the chunks have the same stuck status, check which chunk has the lower
	// completion percentage.
	if uch[i].stuck == uch[j].stuck {
		return float64(uch[i].piecesCompleted)/float64(uch[i].piecesNeeded) < float64(uch[j].piecesCompleted)/float64(uch[j].piecesNeeded)
	}
	// If chunk i is stuck, return true to prioritize it.
	if uch[i].stuck {
		return true
	}
	// Chunk j is stuck, return false to prioritize it.
	return false
}
func (uch uploadChunkHeap) Swap(i, j int)       { uch[i], uch[j] = uch[j], uch[i] }
func (uch *uploadChunkHeap) Push(x interface{}) { *uch = append(*uch, x.(*unfinishedUploadChunk)) }
func (uch *uploadChunkHeap) Pop() interface{} {
	old := *uch
	n := len(old)
	x := old[n-1]
	*uch = old[0 : n-1]
	return x
}

// uploadHeap contains a priority-sorted heap of all the chunks being uploaded
// to the renter, along with some metadata.
type uploadHeap struct {
	heap uploadChunkHeap

	// heapChunks is a map containing all the chunks that are currently in the
	// heap. Chunks are added and removed from the map when chunks are pushed
	// and popped off the heap
	//
	// repairingChunks is a map containing all the chunks are that currently
	// assigned to workers and are being repaired/worked on.
	heapChunks      map[uploadChunkID]struct{}
	repairingChunks map[uploadChunkID]struct{}

	// Control channels
	newUploads        chan struct{}
	repairNeeded      chan struct{}
	stuckChunkFound   chan struct{}
	stuckChunkSuccess chan modules.SiaPath

	mu sync.Mutex
}

// managedLen will return the length of the heap
func (uh *uploadHeap) managedLen() int {
	uh.mu.Lock()
	uhLen := uh.heap.Len()
	uh.mu.Unlock()
	return uhLen
}

// managedPush will try and add a chunk to the upload heap. If the chunk is
// added it will return true otherwise it will return false
func (uh *uploadHeap) managedPush(uuc *unfinishedUploadChunk) bool {
	// Check whether this chunk is already being repaired. If not, add it to the
	// upload chunk heap.
	var added bool
	uh.mu.Lock()
	_, exists1 := uh.heapChunks[uuc.id]
	_, exists2 := uh.repairingChunks[uuc.id]
	if !exists1 && !exists2 {
		uh.heapChunks[uuc.id] = struct{}{}
		uh.heap.Push(uuc)
		added = true
	}
	uh.mu.Unlock()
	return added
}

// managedPop will pull a chunk off of the upload heap and return it.
func (uh *uploadHeap) managedPop() (uc *unfinishedUploadChunk) {
	uh.mu.Lock()
	if len(uh.heap) > 0 {
		uc = heap.Pop(&uh.heap).(*unfinishedUploadChunk)
		delete(uh.heapChunks, uc.id)
	}
	uh.mu.Unlock()
	return uc
}

// buildUnfinishedChunks will pull all of the unfinished chunks out of a file.
//
// NOTE: each unfinishedUploadChunk needs its own SiaFileSetEntry. This is due
// to the SiaFiles being removed from memory. Since the renter does not keep the
// SiaFiles in memory the unfinishedUploadChunks need to close the SiaFile when
// they are done and so cannot share a SiaFileSetEntry as the first chunk to
// finish would then close the Entry and consequentially impact the remaining
// chunks.
//
// TODO / NOTE: This code can be substantially simplified once the files store
// the HostPubKey instead of the FileContractID, and can be simplified even
// further once the layout is per-chunk instead of per-filecontract.
func (r *Renter) buildUnfinishedChunks(entry *siafile.SiaFileSetEntry, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) []*unfinishedUploadChunk {
	// If we don't have enough workers for the file, don't repair it right now.
	if len(r.workerPool) < entry.ErasureCode().MinPieces() {
		r.log.Println("Not building any chunks from file as there are not enough workers, marking all unhealthy chunks as stuck")
		// Mark all unhealthy chunks as stuck
		if err := entry.MarkAllUnhealthyChunksAsStuck(offline, goodForRenew); err != nil {
			r.log.Println("WARN: unable to mark all chunks as stuck:", err)
		}
		return nil
	}

	// Assemble chunk indexes, stuck Loop should only be adding stuck chunks and
	// the repair loop should only be adding unstuck chunks
	var chunkIndexes []uint64
	for i := uint64(0); i < entry.NumChunks(); i++ {
		if (target == targetStuckChunks) == entry.StuckChunkByIndex(i) {
			chunkIndexes = append(chunkIndexes, i)
		}
	}

	// Sanity check that we have chunk indices to go through
	if len(chunkIndexes) == 0 {
		r.log.Println("WARN: no chunk indices gathered, can't add chunks to heap")
		return nil
	}

	// Assemble the set of chunks.
	//
	// TODO / NOTE: Future files may have a different method for determining the
	// number of chunks. Changes will be made due to things like sparse files,
	// and the fact that chunks are going to be different sizes.
	newUnfinishedChunks := make([]*unfinishedUploadChunk, len(chunkIndexes))
	for i, index := range chunkIndexes {
		// Sanity check: fileUID should not be the empty value.
		if entry.UID() == "" {
			build.Critical("empty string for file UID")
		}

		// Create unfinishedUploadChunk
		newUnfinishedChunks[i] = &unfinishedUploadChunk{
			fileEntry: entry.CopyEntry(),

			id: uploadChunkID{
				fileUID: entry.UID(),
				index:   uint64(index),
			},

			index:  uint64(index),
			length: entry.ChunkSize(),
			offset: int64(uint64(index) * entry.ChunkSize()),

			// memoryNeeded has to also include the logical data, and also
			// include the overhead for encryption.
			//
			// TODO / NOTE: If we adjust the file to have a flexible encryption
			// scheme, we'll need to adjust the overhead stuff too.
			//
			// TODO: Currently we request memory for all of the pieces as well
			// as the minimum pieces, but we perhaps don't need to request all
			// of that.
			memoryNeeded:  entry.PieceSize()*uint64(entry.ErasureCode().NumPieces()+entry.ErasureCode().MinPieces()) + uint64(entry.ErasureCode().NumPieces())*entry.MasterKey().Type().Overhead(),
			minimumPieces: entry.ErasureCode().MinPieces(),
			piecesNeeded:  entry.ErasureCode().NumPieces(),
			stuck:         entry.StuckChunkByIndex(uint64(index)),

			physicalChunkData: make([][]byte, entry.ErasureCode().NumPieces()),

			pieceUsage:  make([]bool, entry.ErasureCode().NumPieces()),
			unusedHosts: make(map[string]struct{}),
		}
		// Every chunk can have a different set of unused hosts.
		for host := range hosts {
			newUnfinishedChunks[i].unusedHosts[host] = struct{}{}
		}
	}

	// Iterate through the pieces of all chunks of the file and mark which
	// hosts are already in use for a particular chunk. As you delete hosts
	// from the 'unusedHosts' map, also increment the 'piecesCompleted' value.
	for i, index := range chunkIndexes {
		pieces, err := entry.Pieces(uint64(index))
		if err != nil {
			r.log.Println("failed to get pieces for building incomplete chunks")
			for _, uc := range newUnfinishedChunks {
				if err := uc.fileEntry.Close(); err != nil {
					r.log.Println("failed to close file:", err)
				}
			}
			return nil
		}
		for pieceIndex, pieceSet := range pieces {
			for _, piece := range pieceSet {
				// Get the contract for the piece.
				contractUtility, exists := r.hostContractor.ContractUtility(piece.HostPubKey)
				if !exists {
					// File contract does not seem to be part of the host anymore.
					continue
				}
				if !contractUtility.GoodForRenew {
					// We are no longer renewing with this contract, so it does not
					// count for redundancy.
					continue
				}

				// Mark the chunk set based on the pieces in this contract.
				_, exists = newUnfinishedChunks[i].unusedHosts[piece.HostPubKey.String()]
				redundantPiece := newUnfinishedChunks[i].pieceUsage[pieceIndex]
				if exists && !redundantPiece {
					newUnfinishedChunks[i].pieceUsage[pieceIndex] = true
					newUnfinishedChunks[i].piecesCompleted++
					delete(newUnfinishedChunks[i].unusedHosts, piece.HostPubKey.String())
				} else if exists {
					// This host has a piece, but it is the same piece another
					// host has. We should still remove the host from the
					// unusedHosts since one host having multiple pieces of a
					// chunk might lead to unexpected issues. e.g. if a host
					// has multiple pieces and another host with redundant
					// pieces goes offline, we end up with false redundancy
					// reporting.
					delete(newUnfinishedChunks[i].unusedHosts, piece.HostPubKey.String())
				}
			}
		}
	}

	// Iterate through the set of newUnfinishedChunks and remove any that are
	// completed or are not downloadable.
	incompleteChunks := newUnfinishedChunks[:0]
	for _, chunk := range newUnfinishedChunks {
		// Check if chunk is complete
		incomplete := chunk.piecesCompleted < chunk.piecesNeeded
		// Check if chunk is downloadable
		chunkHealth := chunk.fileEntry.ChunkHealth(int(chunk.index), offline, goodForRenew)
		_, err := os.Stat(chunk.fileEntry.LocalPath())
		downloadable := chunkHealth <= 1 || err == nil
		// Check if chunk seems stuck
		stuck := !incomplete && chunkHealth != 0

		// Add chunk to list of incompleteChunks if it is incomplete and
		// downloadable or if we are targetting stuck chunks
		if incomplete && (downloadable || target == targetStuckChunks) {
			incompleteChunks = append(incompleteChunks, chunk)
			continue
		}

		// If a chunk is not downloadable mark it as stuck
		if !downloadable {
			r.log.Println("Marking chunk", chunk.id, "as stuck due to not being downloadable")
			err = chunk.fileEntry.SetStuck(chunk.index, true)
			if err != nil {
				r.log.Println("WARN: unable to mark chunk as stuck:", err)
			}
			continue
		} else if stuck {
			r.log.Println("Marking chunk", chunk.id, "as stuck due to being complete but having a health of", chunkHealth)
			err = chunk.fileEntry.SetStuck(chunk.index, true)
			if err != nil {
				r.log.Println("WARN: unable to mark chunk as stuck:", err)
			}
			continue
		}

		// Close entry of completed chunk
		err = r.managedSetStuckAndClose(chunk, false)
		if err != nil {
			r.log.Debugln("WARN: unable to mark chunk as stuck and close:", err)
		}
	}
	return incompleteChunks
}

// managedBuildAndPushRandomChunk randomly selects a file and builds the
// unfinished chunks, then randomly adds one chunk to the upload heap
func (r *Renter) managedBuildAndPushRandomChunk(files []*siafile.SiaFileSetEntry, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) {
	// Sanity check that there are files
	if len(files) == 0 {
		return
	}
	// Grab a random file
	randFileIndex := fastrand.Intn(len(files))
	file := files[randFileIndex]
	id := r.mu.Lock()
	// Build the unfinished stuck chunks from the file
	unfinishedUploadChunks := r.buildUnfinishedChunks(file, hosts, target, offline, goodForRenew)
	r.mu.Unlock(id)
	// Sanity check that there are stuck chunks
	if len(unfinishedUploadChunks) == 0 {
		r.log.Println("WARN: no stuck unfinishedUploadChunks returned from buildUnfinishedChunks, so no stuck chunks will be added to the heap")
		return
	}
	// Add a random stuck chunk to the upload heap and set its stuckRepair field
	// to true
	randChunkIndex := fastrand.Intn(len(unfinishedUploadChunks))
	randChunk := unfinishedUploadChunks[randChunkIndex]
	randChunk.stuckRepair = true
	if !r.uploadHeap.managedPush(randChunk) {
		// Chunk wasn't added to the heap. Close the file
		err := randChunk.fileEntry.Close()
		if err != nil {
			r.log.Println("WARN: unable to close file:", err)
		}
	}
	// Close the unused unfinishedUploadChunks
	unfinishedUploadChunks = append(unfinishedUploadChunks[:randChunkIndex], unfinishedUploadChunks[randChunkIndex+1:]...)
	for _, chunk := range unfinishedUploadChunks {
		err := chunk.fileEntry.Close()
		if err != nil {
			r.log.Println("WARN: unable to close file:", err)
		}
	}
	return
}

// managedBuildAndPushChunks builds the unfinished upload chunks and adds them
// to the upload heap
func (r *Renter) managedBuildAndPushChunks(files []*siafile.SiaFileSetEntry, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) {
	// Loop through the whole set of files and get a list of chunks to add to
	// the heap.
	for _, file := range files {
		id := r.mu.Lock()
		unfinishedUploadChunks := r.buildUnfinishedChunks(file, hosts, target, offline, goodForRenew)
		r.mu.Unlock(id)
		if len(unfinishedUploadChunks) == 0 {
			r.log.Println("No unfinishedUploadChunks returned from buildUnfinishedChunks, so no chunks will be added to the heap")
			continue
		}
		for i := 0; i < len(unfinishedUploadChunks); i++ {
			if !r.uploadHeap.managedPush(unfinishedUploadChunks[i]) {
				// Chunk wasn't added to the heap. Close the file
				err := unfinishedUploadChunks[i].fileEntry.Close()
				if err != nil {
					r.log.Println("WARN: unable to close file:", err)
				}
			}
		}
	}
}

// managedBuildChunkHeap will iterate through all of the files in the renter and
// construct a chunk heap.
func (r *Renter) managedBuildChunkHeap(dirSiaPath modules.SiaPath, hosts map[string]struct{}, target repairTarget) {
	// Get Directory files
	var files []*siafile.SiaFileSetEntry
	var err error
	fileinfos, err := ioutil.ReadDir(dirSiaPath.SiaDirSysPath(r.staticFilesDir))
	if err != nil {
		return
	}
	for _, fi := range fileinfos {
		// skip sub directories and non siafiles
		ext := filepath.Ext(fi.Name())
		if fi.IsDir() || ext != modules.SiaFileExtension {
			continue
		}

		// Open SiaFile
		siaPath, err := dirSiaPath.Join(strings.TrimSuffix(fi.Name(), ext))
		if err != nil {
			return
		}
		file, err := r.staticFileSet.Open(siaPath)
		if err != nil {
			r.log.Println("WARN: could not open siafile:", err)
			continue
		}

		// For stuck chunk repairs, check to see if file has stuck chunks
		if target == targetStuckChunks && file.NumStuckChunks() == 0 {
			// Close unneeded files
			err := file.Close()
			if err != nil {
				r.log.Println("WARN: Could not close file:", err)
			}
			continue
		}

		// For normal repairs, ignore files that have been recently repaired
		if target == targetUnstuckChunks && time.Since(file.RecentRepairTime()) < fileRepairInterval {
			// Close unneeded files
			err := file.Close()
			if err != nil {
				r.log.Println("WARN: Could not close file:", err)
			}
			continue
		}
		// For normal repairs, ignore files that don't have any unstuck chunks
		if target == targetUnstuckChunks && file.NumChunks() == file.NumStuckChunks() {
			err := file.Close()
			if err != nil {
				r.log.Println("WARN: Could not close file:", err)
			}
			continue
		}

		files = append(files, file)
	}

	// Check if any files were selected from directory
	if len(files) == 0 {
		r.log.Debugln("No files pulled from `", dirSiaPath, "` to build the repair heap")
		return
	}

	// Build the unfinished upload chunks and add them to the upload heap
	offline, goodForRenew, _ := r.managedContractUtilityMaps()
	switch target {
	case targetStuckChunks:
		r.log.Debugln("Adding stuck chunk to heap")
		r.managedBuildAndPushRandomChunk(files, hosts, target, offline, goodForRenew)
	case targetUnstuckChunks:
		r.log.Debugln("Adding chunks to heap")
		r.managedBuildAndPushChunks(files, hosts, target, offline, goodForRenew)
	default:
		r.log.Println("WARN: repair target not recognized", target)
	}

	// Close all files
	for _, file := range files {
		err := file.Close()
		if err != nil {
			r.log.Println("WARN: Could not close file:", err)
		}
	}
}

// managedPrepareNextChunk takes the next chunk from the chunk heap and prepares
// it for upload. Preparation includes blocking until enough memory is
// available, fetching the logical data for the chunk (either from the disk or
// from the network), erasure coding the logical data into the physical data,
// and then finally passing the work onto the workers.
func (r *Renter) managedPrepareNextChunk(uuc *unfinishedUploadChunk, hosts map[string]struct{}) error {
	// Grab the next chunk, loop until we have enough memory, update the amount
	// of memory available, and then spin up a thread to asynchronously handle
	// the rest of the chunk tasks.
	if !r.memoryManager.Request(uuc.memoryNeeded, memoryPriorityLow) {
		return errors.New("couldn't request memory")
	}
	// Fetch the chunk in a separate goroutine, as it can take a long time and
	// does not need to bottleneck the repair loop.
	go r.threadedFetchAndRepairChunk(uuc)
	return nil
}

// managedRefreshHostsAndWorkers will reset the set of hosts and the set of
// workers for the renter.
func (r *Renter) managedRefreshHostsAndWorkers() map[string]struct{} {
	// Grab the current set of contracts and use them to build a list of hosts
	// that are currently active. The hosts are assembled into a map where the
	// key is the String() representation of the host's SiaPublicKey.
	//
	// TODO / NOTE: This code can be removed once files store the HostPubKey
	// of the hosts they are using, instead of just the FileContractID.
	currentContracts := r.hostContractor.Contracts()
	hosts := make(map[string]struct{})
	for _, contract := range currentContracts {
		hosts[contract.HostPublicKey.String()] = struct{}{}
	}
	// Refresh the worker pool as well.
	r.managedUpdateWorkerPool()
	return hosts
}

// managedRepairLoop works through the upload heap repairing chunks. The repair
// loop will continue until the renter stops, there are no more chunks, or
// enough time has passed indicated by the rebuildHeapSignal
func (r *Renter) managedRepairLoop(hosts map[string]struct{}) {
	var consecutiveChunkRepairs int
	rebuildHeapSignal := time.After(rebuildChunkHeapInterval)
	for {
		select {
		case <-r.tg.StopChan():
			// Return if the renter has shut down.
			return
		case <-rebuildHeapSignal:
			// Return if workers/heap need to be refreshed.
			return
		default:
		}

		// Return if not online.
		if !r.g.Online() {
			return
		}

		// Check if there is work by trying to pop of the next chunk from
		// the heap.
		nextChunk := r.uploadHeap.managedPop()
		if nextChunk == nil {
			return
		}
		r.log.Debugln("Sending next chunk to the workers", nextChunk.id)

		// Make sure we have enough workers for this chunk to reach minimum
		// redundancy. Otherwise we ignore this chunk for now, mark it as stuck
		// and let the stuck loop work on it
		id := r.mu.RLock()
		availableWorkers := len(r.workerPool)
		r.mu.RUnlock(id)
		if availableWorkers < nextChunk.minimumPieces {
			// Not enough available workers, mark as stuck and close
			r.log.Debugln("Setting chunk  as stuck because there are not enough good workers", nextChunk.id)
			err := r.managedSetStuckAndClose(nextChunk, true)
			if err != nil {
				r.log.Debugln("WARN: unable to mark chunk as stuck and close:", err)
			}
			continue
		}

		// Perform the work. managedPrepareNextChunk will block until
		// enough memory is available to perform the work, slowing this
		// thread down to using only the resources that are available.
		err := r.managedPrepareNextChunk(nextChunk, hosts)
		if err != nil {
			// We were unsuccessful in preparing the next chunk so we need to
			// mark the chunk as stuck and close the file
			r.log.Debugln("WARN: unable to prepare next chunk without issues", err, nextChunk.id)
			err = r.managedSetStuckAndClose(nextChunk, true)
			if err != nil {
				r.log.Debugln("WARN: unable to mark chunk as stuck and close:", err)
			}
			continue
		}
		consecutiveChunkRepairs++

		// Check if enough chunks are currently being repaired
		if consecutiveChunkRepairs >= maxConsecutiveChunkRepairs {
			// Pull all of the chunks out of the heap and return. Save the stuck
			// chunks, as this is the repair loop and we aren't trying to erase
			// the stuck chunks.
			var stuckChunks []*unfinishedUploadChunk
			for r.uploadHeap.managedLen() > 0 {
				if c := r.uploadHeap.managedPop(); c.stuck {
					stuckChunks = append(stuckChunks, c)
				} else {
					// Unstuck chunks are not added back and need to be closed.
					err = c.fileEntry.Close()
					if err != nil {
						r.log.Println("WARN: unable to close file:", err)
					}
				}
			}
			for _, sc := range stuckChunks {
				if !r.uploadHeap.managedPush(sc) {
					// Chunk wasn't added to the heap. Close the file
					err := sc.fileEntry.Close()
					if err != nil {
						r.log.Println("WARN: unable to close file:", err)
					}
				}
			}
			return
		}
	}
}

// managedUploadAndRepair will find new uploads and existing files in need of
// repair and execute the uploads and repairs. This function effectively runs a
// single iteration of threadedUploadAndRepair.
func (r *Renter) managedUploadAndRepair() error {
	// Find the lowest health directory to queue for repairs.
	dirSiaPath, dirHealth, err := r.managedWorstHealthDirectory()
	if err != nil {
		r.log.Println("WARN: error getting worst health directory:", err)
		return err
	}

	// Refresh the worker pool and get the set of hosts that are currently
	// useful for uploading.
	hosts := r.managedRefreshHostsAndWorkers()

	// Build a min-heap of chunks organized by upload progress.
	r.managedBuildChunkHeap(dirSiaPath, hosts, targetUnstuckChunks)
	r.uploadHeap.mu.Lock()
	heapLen := r.uploadHeap.heap.Len()
	r.uploadHeap.mu.Unlock()
	if heapLen == 0 {
		r.log.Debugf("No chunks added to the heap for repair from `%v` even through health was %v", dirSiaPath, dirHealth)
		// Call threadedBubble to make sure that directory information is
		// accurate
		r.threadedBubbleMetadata(dirSiaPath)
		return nil
	}
	r.log.Println("Repairing", heapLen, "chunks from", dirSiaPath)

	// Work through the heap and repair files
	r.managedRepairLoop(hosts)

	// Once we have worked through the heap, call bubble to update the
	// directory metadata
	r.threadedBubbleMetadata(dirSiaPath)
	return nil
}

// threadedUploadAndRepair is a background thread that maintains a queue of
// chunks to repair. This thread attempts to prioritize repairing files and
// chunks with the lowest health, and attempts to keep heavy throughput
// sustained for data upload as long as there is at least one chunk in need of
// upload or repair.
func (r *Renter) threadedUploadAndRepair() {
	err := r.tg.Add()
	if err != nil {
		return
	}
	defer r.tg.Done()

	// Perpetual loop to scan for more files.
	for {
		// Return if the renter has shut down.
		select {
		case <-r.tg.StopChan():
			return
		default:
		}

		// Wait until the renter is online to proceed. This function will return
		// 'false' if the renter has shut down before being online.
		if !r.managedBlockUntilOnline() {
			return
		}

		// Check whether a repair is needed. If a repair is not needed, block
		// until there is a signal suggesting that a repair is needed. If there
		// is a new upload, a signal will be sent through the 'newUploads'
		// channel, and if the metadata updating code finds a file that needs
		// repairing, a signal is sent through the 'repairNeeded' channel.
		rootMetadata, err := r.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			// If there is an error fetching the root directory metadata, sleep
			// for a bit and hope that on the next iteration, things will be
			// better.
			r.log.Println("WARN: error fetching filesystem root metadata:", err)
			select {
			case <-time.After(uploadAndRepairErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}
		if rootMetadata.Health < siafile.RemoteRepairDownloadThreshold {
			// Block until a signal is received that there is more work to do.
			// A signal will be sent if new data to upload is received, or if
			// the health loop discovers that some files are not in good health.
			select {
			case <-r.uploadHeap.newUploads:
			case <-r.uploadHeap.repairNeeded:
			case <-r.tg.StopChan():
				return
			}
			continue
		}

		// The necessary conditions for performing an upload and repair
		// iteration have been met - perform an upload and repair iteration.
		err = r.managedUploadAndRepair()
		if err != nil {
			// If there is an error performing an upload and repair iteration,
			// sleep for a bit and hope that on the next iteration, things will
			// be better.
			r.log.Println("WARN: error performing upload and repair iteration:", err)
			select {
			case <-time.After(uploadAndRepairErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
		}

		// TODO: This sleep is a hack to keep the CPU from spinning at 100% for
		// a brief time when all of the chunks in the directory have been added
		// to the repair loop, but the directory isn't full yet so it keeps
		// trying to add more chunks.
		time.Sleep(20 * time.Millisecond)
	}
}
