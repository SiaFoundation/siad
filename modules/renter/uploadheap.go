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
	repairingChunks   map[uploadChunkID]struct{}
	stuckHeapChunks   map[uploadChunkID]struct{}
	unstuckHeapChunks map[uploadChunkID]struct{}

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
	var added bool
	// Grab chunk stuck status
	uuc.mu.Lock()
	chunkStuck := uuc.stuck
	uuc.mu.Unlock()

	// Check if chunk is in any of the heap maps
	uh.mu.Lock()
	_, existsUnstuckHeap := uh.unstuckHeapChunks[uuc.id]
	_, existsRepairing := uh.repairingChunks[uuc.id]
	_, existsStuckHeap := uh.stuckHeapChunks[uuc.id]

	// Check if the chunk can be added to the heap
	canAddStuckChunk := chunkStuck && !existsStuckHeap && !existsRepairing && len(uh.stuckHeapChunks) < maxStuckChunksInHeap
	canAddUnstuckChunk := !chunkStuck && !existsUnstuckHeap && !existsRepairing
	if canAddStuckChunk {
		uh.stuckHeapChunks[uuc.id] = struct{}{}
		heap.Push(&uh.heap, uuc)
		added = true
	} else if canAddUnstuckChunk {
		uh.unstuckHeapChunks[uuc.id] = struct{}{}
		heap.Push(&uh.heap, uuc)
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
		delete(uh.unstuckHeapChunks, uc.id)
		delete(uh.stuckHeapChunks, uc.id)
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
	minPieces := entry.ErasureCode().MinPieces()
	if len(r.workerPool) < minPieces {
		// There are not enough workers for the chunk to reach minimum
		// redundancy. Check if the allowance has enough hosts for the chunk to
		// reach minimum redundancy
		r.log.Debugln("Not building any chunks from file as there are not enough workers")
		allowance := r.hostContractor.Allowance()
		// Only perform this check when we are looking for unstuck chunks. This
		// will prevent log spam from repeatedly logging to the user the issue
		// with the file after marking the chunks as stuck
		if allowance.Hosts < uint64(minPieces) && target == targetUnstuckChunks {
			// There are not enough hosts in the allowance for the file to reach
			// minimum redundancy. Mark all unhealthy chunks as stuck
			r.log.Printf("WARN: allownace had insufficient hosts for chunk to reach minimum redundancy, have %v need %v for file %v", allowance.Hosts, minPieces, entry.SiaFilePath())
			if err := entry.MarkAllUnhealthyChunksAsStuck(offline, goodForRenew); err != nil {
				r.log.Println("WARN: unable to mark all chunks as stuck:", err)
			}
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
		// Check the chunk status. A chunk is repairable if it can be fully
		// downloaded, or if the source file is available on disk. We also check
		// if the chunk needs repair, which is only true if more than a certain
		// amount of redundancy is missing. We only repair above a certain
		// threshold of missing redundancy to minimize the amount of repair work
		// that gets triggered by host churn.
		chunkHealth := chunk.fileEntry.ChunkHealth(int(chunk.index), offline, goodForRenew)
		_, err := os.Stat(chunk.fileEntry.LocalPath())
		// While a file could be on disk as long as !os.IsNotExist(err), for the
		// purposes of repairing a file is only considered on disk if it can be
		// accessed without error. If there is an error accessing the file then
		// it is likely that we can not read the file in which case it can not
		// be used for repair.
		onDisk := err == nil
		repairable := chunkHealth <= 1 || onDisk
		needsRepair := chunkHealth >= siafile.RemoteRepairDownloadThreshold

		// Add chunk to list of incompleteChunks if it is incomplete and
		// repairable or if we are targetting stuck chunks
		if needsRepair && (repairable || target == targetStuckChunks) {
			incompleteChunks = append(incompleteChunks, chunk)
			continue
		}

		// If a chunk is not able to be repaired, mark it as stuck.
		if !repairable {
			r.log.Println("Marking chunk", chunk.id, "as stuck due to not being downloadable")
			err = r.managedSetStuckAndClose(chunk, true)
			if err != nil {
				r.log.Debugln("WARN: unable to set chunk stuck status and close:", err)
			}
			continue
		}

		// Close entry of completed chunk
		err = r.managedSetStuckAndClose(chunk, false)
		if err != nil {
			r.log.Debugln("WARN: unable to set chunk stuck status and close:", err)
		}
	}
	return incompleteChunks
}

// managedAddChunksToHeap will add chunks to the upload heap from a directory
// that is in need of repair. It does this by finding the worst health directory
// and adding the chunks from that directory to the upload heap. If the worst
// health directory found is sufficiently healthy then no chunks will be added
// to the heap
func (r *Renter) managedAddChunksToHeap(hosts map[string]struct{}) (modules.SiaPath, float64, error) {
	// Pop an explored directory off of the directory heap
	dir, err := r.managedNextExploredDirectory()
	if err != nil {
		r.log.Println("WARN: error getting explored directory:", err)
		return modules.SiaPath{}, 0, err
	}

	// Grab health and siaPath of the directory
	dir.mu.Lock()
	dirHealth := dir.health
	dirSiaPath := dir.siaPath
	dir.mu.Unlock()

	// If the lowest health directory is healthy then return
	if dirHealth < siafile.RemoteRepairDownloadThreshold {
		return dirSiaPath, dirHealth, nil
	}

	// Build a min-heap of chunks organized by upload progress.
	r.managedBuildChunkHeap(dirSiaPath, hosts, targetUnstuckChunks)
	heapLen := r.uploadHeap.managedLen()
	if heapLen == 0 {
		r.log.Debugf("No chunks added to the heap for repair from `%v` even through health was %v", dirSiaPath, dirHealth)
		return dirSiaPath, dirHealth, nil
	}
	r.log.Println("Repairing", heapLen, "chunks from", dirSiaPath)

	return dirSiaPath, dirHealth, nil
}

// managedBuildAndPushRandomChunk randomly selects a file and builds the
// unfinished chunks, then randomly adds chunksToAdd chunks to the upload heap
func (r *Renter) managedBuildAndPushRandomChunk(files []*siafile.SiaFileSetEntry, chunksToAdd int, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) {
	// Sanity check that there are files
	if len(files) == 0 {
		return
	}

	// Create random indices for files
	p := fastrand.Perm(len(files))
	for i := 0; i < chunksToAdd && i < len(files); i++ {
		// Grab random file
		file := files[p[i]]

		// Build the unfinished stuck chunks from the file
		id := r.mu.Lock()
		unfinishedUploadChunks := r.buildUnfinishedChunks(file, hosts, target, offline, goodForRenew)
		r.mu.Unlock(id)

		// Sanity check that there are stuck chunks
		if len(unfinishedUploadChunks) == 0 {
			continue
		}

		// Add random stuck chunks to the upload heap and set its stuckRepair field
		// to true
		randChunkIndex := fastrand.Intn(len(unfinishedUploadChunks))
		randChunk := unfinishedUploadChunks[randChunkIndex]
		randChunk.stuckRepair = true
		if !r.uploadHeap.managedPush(randChunk) {
			// Chunk wasn't added to the heap. Close the file
			r.log.Debugln("WARN: stuck chunk", randChunk.id, "wasn't added to heap")
			err := randChunk.fileEntry.Close()
			if err != nil {
				r.log.Println("WARN: unable to close file:", err)
			}
		}
		unfinishedUploadChunks = append(unfinishedUploadChunks[:randChunkIndex], unfinishedUploadChunks[randChunkIndex+1:]...)
		// Close the unused unfinishedUploadChunks
		for _, chunk := range unfinishedUploadChunks {
			err := chunk.fileEntry.Close()
			if err != nil {
				r.log.Println("WARN: unable to close file:", err)
			}
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
			continue
		}
		for i := 0; i < len(unfinishedUploadChunks); i++ {
			if !r.uploadHeap.managedPush(unfinishedUploadChunks[i]) {
				// Chunk wasn't added to the heap. Close the file.
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
		r.managedBuildAndPushRandomChunk(files, maxStuckChunksInHeap, hosts, target, offline, goodForRenew)
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

// managedRepairLoop works through the uploadheap repairing chunks. The repair
// loop will continue until the renter stops, there are no more chunks, or the
// number of chunks in the uploadheap has dropped below the minUploadHeapSize
func (r *Renter) managedRepairLoop(hosts map[string]struct{}) error {
	// smallRepair indicates whether or not the repair loop is starting off
	// below minUploadHeapSize. This is the case with small file uploads, small
	// file repairs, or repairs on mostly healthy file systems. In these cases
	// we want to just drain the heap
	smallRepair := r.uploadHeap.managedLen() < minUploadHeapSize

	// Work through the heap repairing chunks until heap is empty for
	// smallRepairs or heap drops below minUploadHeapSize for larger repairs
	for r.uploadHeap.managedLen() >= minUploadHeapSize || smallRepair {
		select {
		case <-r.tg.StopChan():
			// Return if the renter has shut down.
			return errors.New("Repair loop interrupted because renter is shutting down")
		default:
		}

		// Return if the renter is not online.
		if !r.g.Online() {
			return errors.New("repair loop returned early due to the renter been offline")
		}

		// Check if there is work by trying to pop of the next chunk from
		// the heap.
		nextChunk := r.uploadHeap.managedPop()
		if nextChunk == nil {
			// The heap is empty so return
			return nil
		}
		r.log.Debugln("Sending next chunk to the workers", nextChunk.id)

		// Make sure we have enough workers for this chunk to reach minimum
		// redundancy.
		id := r.mu.RLock()
		availableWorkers := len(r.workerPool)
		r.mu.RUnlock(id)
		if availableWorkers < nextChunk.minimumPieces {
			// There are not enough available workers for the chunk to reach
			// minimum redundancy. Check if the allowance has enough hosts for
			// the chunk to reach minimum redundancy
			allowance := r.hostContractor.Allowance()
			// Only perform this check on chunks that are not stuck to prevent
			// log spam
			if allowance.Hosts < uint64(nextChunk.minimumPieces) && !nextChunk.stuck {
				// There are not enough hosts in the allowance for this chunk to
				// reach minimum redundancy. Log an error, set the chunk as stuck,
				// and close the file
				r.log.Printf("WARN: allownace had insufficient hosts for chunk to reach minimum redundancy, have %v need %v for chunk %v", allowance.Hosts, nextChunk.minimumPieces, nextChunk.id)
				err := nextChunk.fileEntry.SetStuck(nextChunk.index, true)
				if err != nil {
					r.log.Debugln("WARN: unable to mark chunk as stuck:", err, nextChunk.id)
				}
			}
			// There are enough hosts set in the allowance so this is a
			// temporary issue with available workers, just ignore the chunk
			// for now and close the file
			err := nextChunk.fileEntry.Close()
			if err != nil {
				r.log.Debugln("WARN: unable to close file:", err, nextChunk.fileEntry.SiaFilePath())
			}
			continue
		}

		// Perform the work. managedPrepareNextChunk will block until
		// enough memory is available to perform the work, slowing this
		// thread down to using only the resources that are available.
		err := r.managedPrepareNextChunk(nextChunk, hosts)
		if err != nil {
			// An error was return which means the renter was unable to allocate
			// memory for the repair. Since that is not an issue with the file
			// we will just close the chunk file entry instead of marking it as
			// stuck
			r.log.Debugln("WARN: unable to prepare next chunk without issues", err, nextChunk.id)
			err = nextChunk.fileEntry.Close()
			if err != nil {
				r.log.Debugln("WARN: unable to close file:", err, nextChunk.fileEntry.SiaFilePath())
			}
			continue
		}
	}
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

	if r.deps.Disrupt("InterruptRepairAndStuckLoops") {
		return
	}

	// Perpetual loop to scan for more files and add chunks to the uploadheap
	for {
		// Return if the renter has shut down
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

		// Refresh the worker pool and get the set of hosts that are currently
		// useful for uploading.
		hosts := r.managedRefreshHostsAndWorkers()

		// Add chunks to heap
		dirSiaPath, dirHealth, err := r.managedAddChunksToHeap(hosts)
		if err != nil {
			// If there was an error adding chunks to the heap sleep for a
			// little bit and then try again
			r.log.Debugln("WARN: error adding chunks to the heap:", err)
			select {
			case <-time.After(uploadAndRepairErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
			continue
		}

		// Check if the file system is healthy
		if dirHealth < siafile.RemoteRepairDownloadThreshold {
			// If the file system is healthy then block until there is a new
			// upload or there is a repair that is needed.
			select {
			case <-r.uploadHeap.newUploads:
				// Since uploads are added directly to the heap then we want to
				// move straight to the repair instead of continuing to the next
				// iteration of the for loop. If we continue to the next
				// iteration of the repair loop the filesystem metadata might
				// not be updated yet and it might appear to be healthy and
				// therefore not begin the repair/upload.
			case <-r.uploadHeap.repairNeeded:
				// Since the repairNeeded channel is used by the stuck loop to
				// indicate that a stuck chunk has been added to the uploadheap,
				// we want to move straight to the repair instead of continuing
				// to the next iteration of the for loop. If we continue to the
				// next iteration of the repair loop we will end up back here as
				// stuck chunks are not considered in the Health of the
				// filesystem so the filesystem will still appear to be healthy.
			case <-r.tg.StopChan():
				return
			}
			// Refresh directory heap
			err = r.managedResetDirectoryHeap()
			if err != nil {
				r.log.Panicln("WARN: there was an error reseting the directory heap:", err)
			}
			// Make sure that the hosts and workers are updated before
			// continuing to the repair loop
			hosts = r.managedRefreshHostsAndWorkers()
		}

		// The necessary conditions for performing an upload and repair have
		// been met - perform the upload and repair by having the repair loop
		// work through the chunks in the uploadheap
		r.log.Debugln("Executing an upload and repair cycle")
		err = r.managedRepairLoop(hosts)
		if err != nil {
			// If there was an error with the repair loop sleep for a little bit
			// and then try again
			r.log.Println("WARN: there was an error in the repair loop:", err)
			select {
			case <-time.After(uploadAndRepairErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
		}

		// Call managedBubbleMetadata to update the filesystem
		err = r.managedBubbleMetadata(dirSiaPath)
		if err != nil {
			// If there is an error with calling bubble log an error and then
			// continue. Since bubble is called from a variety of places it is
			// fine to just continue on without any special handling
			r.log.Debugf("WARN: error calling managedBubbleMetadata on %v, err: %v", dirSiaPath, err)
		}
	}
}
