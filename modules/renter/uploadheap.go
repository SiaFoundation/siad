package renter

// TODO: replace managedRefreshHostsAndWorkers with structural updates to the
// worker pool. The worker pool should maintain the map of hosts that
// managedRefreshHostsAndWorkers builds every call, and the contractor should
// work with the worker pool to instantly notify the worker pool of any changes
// to the set of contracts.

import (
	"container/heap"
	"io/ioutil"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/types"
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
	targetBackupChunks
)

// uploadChunkHeap is a bunch of priority-sorted chunks that need to be either
// uploaded or repaired.
type uploadChunkHeap []*unfinishedUploadChunk

// Implementation of heap.Interface for uploadChunkHeap.
func (uch uploadChunkHeap) Len() int { return len(uch) }
func (uch uploadChunkHeap) Less(i, j int) bool {
	// If only chunk i is high priority, return true to prioritize it.
	if uch[i].priority && !uch[j].priority {
		return true
	}
	// If only chunk j is high priority, return false to prioritize it.
	if !uch[i].priority && uch[j].priority {
		return false
	}
	// If the chunks have the same stuck status, check which chunk has the worse
	// health. A higher health is a worse health
	if uch[i].stuck == uch[j].stuck {
		return uch[i].health > uch[j].health
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
	*uch = old[:n-1]
	return x
}

// reset clears the uploadChunkHeap and makes sure all the files belonging to
// the chunks are closed
func (uch *uploadChunkHeap) reset() (err error) {
	for _, c := range *uch {
		err = errors.Compose(err, c.fileEntry.Close())
	}
	*uch = uploadChunkHeap{}
	return err
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
	stuckChunkSuccess chan struct{}

	mu sync.Mutex
}

// managedExists checks if a chunk currently exists in the upload heap. A chunk
// exists in the upload heap if it exists in any of the heap's tracking maps
func (uh *uploadHeap) managedExists(id uploadChunkID) bool {
	uh.mu.Lock()
	defer uh.mu.Unlock()
	_, existsUnstuckHeap := uh.unstuckHeapChunks[id]
	_, existsRepairing := uh.repairingChunks[id]
	_, existsStuckHeap := uh.stuckHeapChunks[id]
	return existsUnstuckHeap || existsRepairing || existsStuckHeap
}

// managedLen will return the length of the heap
func (uh *uploadHeap) managedLen() int {
	uh.mu.Lock()
	uhLen := uh.heap.Len()
	uh.mu.Unlock()
	return uhLen
}

// managedMarkRepairDone removes the chunk from the repairingChunks map of the
// uploadHeap. It also performs a sanity check that the chunk was in the map,
// this is to ensure that we are adding and removing the chunks as expected
func (uh *uploadHeap) managedMarkRepairDone(id uploadChunkID) {
	uh.mu.Lock()
	defer uh.mu.Unlock()
	_, ok := uh.repairingChunks[id]
	if !ok {
		build.Critical("Chunk is not in the repair map, this means it was removed prematurely or was never added")
	}
	delete(uh.repairingChunks, id)
}

// managedNumStuckChunks returns the number of stuck chunks in the heap
func (uh *uploadHeap) managedNumStuckChunks() int {
	uh.mu.Lock()
	defer uh.mu.Unlock()
	return len(uh.stuckHeapChunks)
}

// managedPush will try and add a chunk to the upload heap. If the chunk is
// added it will return true otherwise it will return false
func (uh *uploadHeap) managedPush(uuc *unfinishedUploadChunk) bool {
	// Grab chunk stuck status
	uuc.mu.Lock()
	chunkStuck := uuc.stuck
	uuc.mu.Unlock()

	// Check if chunk is in any of the heap maps
	uh.mu.Lock()
	defer uh.mu.Unlock()
	_, existsUnstuckHeap := uh.unstuckHeapChunks[uuc.id]
	_, existsRepairing := uh.repairingChunks[uuc.id]
	_, existsStuckHeap := uh.stuckHeapChunks[uuc.id]

	// Check if the chunk can be added to the heap
	canAddStuckChunk := chunkStuck && !existsStuckHeap && !existsRepairing && len(uh.stuckHeapChunks) < maxStuckChunksInHeap
	canAddUnstuckChunk := !chunkStuck && !existsUnstuckHeap && !existsRepairing
	if canAddStuckChunk {
		uh.stuckHeapChunks[uuc.id] = struct{}{}
		heap.Push(&uh.heap, uuc)
		return true
	} else if canAddUnstuckChunk {
		uh.unstuckHeapChunks[uuc.id] = struct{}{}
		heap.Push(&uh.heap, uuc)
		return true
	}
	return false
}

// managedPop will pull a chunk off of the upload heap and return it.
func (uh *uploadHeap) managedPop() (uc *unfinishedUploadChunk) {
	uh.mu.Lock()
	if len(uh.heap) > 0 {
		uc = heap.Pop(&uh.heap).(*unfinishedUploadChunk)
		delete(uh.unstuckHeapChunks, uc.id)
		delete(uh.stuckHeapChunks, uc.id)
		if _, exists := uh.repairingChunks[uc.id]; exists {
			build.Critical("There should not be a chunk in the heap that can be popped that is currently being repaired")
		}
		uh.repairingChunks[uc.id] = struct{}{}
	}
	uh.mu.Unlock()
	return uc
}

// managedReset will reset the slice and maps within the heap to free up memory.
func (uh *uploadHeap) managedReset() error {
	uh.mu.Lock()
	defer uh.mu.Unlock()
	uh.unstuckHeapChunks = make(map[uploadChunkID]struct{})
	uh.stuckHeapChunks = make(map[uploadChunkID]struct{})
	return uh.heap.reset()
}

// managedBuildUnfinishedChunk will pull out a single unfinished chunk of a file.
func (r *Renter) managedBuildUnfinishedChunk(entry *siafile.SiaFileSetEntry, chunkIndex uint64, hosts map[string]struct{}, hostPublicKeys map[string]types.SiaPublicKey, priority bool, offline, goodForRenew map[string]bool) (*unfinishedUploadChunk, error) {
	// Copy entry
	entryCopy, err := entry.CopyEntry()
	if err != nil {
		r.log.Println("WARN: unable to copy siafile entry:", err)
		return nil, errors.AddContext(err, "unable to copy file entry when trying to build the unfinished chunk")
	}
	if entryCopy == nil {
		build.Critical("nil file entry return from CopyEntry, and no error should have been returned")
		return nil, errors.New("CopyEntry returned a nil copy")
	}
	stuck, err := entry.StuckChunkByIndex(chunkIndex)
	if err != nil {
		r.log.Println("WARN: unable to get 'stuck' status:", err)
		return nil, errors.AddContext(err, "unable to get 'stuck' status")
	}
	uuc := &unfinishedUploadChunk{
		fileEntry: entryCopy,

		id: uploadChunkID{
			fileUID: entry.UID(),
			index:   chunkIndex,
		},

		index:    chunkIndex,
		length:   entry.ChunkSize(),
		offset:   int64(chunkIndex * entry.ChunkSize()),
		priority: priority,

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
		stuck:         stuck,

		physicalChunkData: make([][]byte, entry.ErasureCode().NumPieces()),

		pieceUsage:  make([]bool, entry.ErasureCode().NumPieces()),
		unusedHosts: make(map[string]struct{}, len(hosts)),
	}

	// Every chunk can have a different set of unused hosts.
	for host := range hosts {
		uuc.unusedHosts[host] = struct{}{}
	}

	// Iterate through the pieces of all chunks of the file and mark which
	// hosts are already in use for a particular chunk. As you delete hosts
	// from the 'unusedHosts' map, also increment the 'piecesCompleted' value.
	pieces, err := entry.Pieces(chunkIndex)
	if err != nil {
		r.log.Println("failed to get pieces for building incomplete chunks", err)
		if err := entry.SetStuck(chunkIndex, true); err != nil {
			r.log.Printf("failed to set chunk %v stuck: %v", chunkIndex, err)
		}
		return nil, errors.AddContext(err, "error trying to get the pieces for the chunk")
	}
	for pieceIndex, pieceSet := range pieces {
		for _, piece := range pieceSet {
			hpk := piece.HostPubKey.String()
			goodForRenew, exists2 := goodForRenew[hpk]
			offline, exists := offline[hpk]
			if !exists || !exists2 || !goodForRenew || offline {
				// This piece cannot be counted towards redudnacy if the host is
				// offline, is marked no good for renew, or is not available in
				// the lookup maps.
				continue
			}

			// Mark the chunk set based on the pieces in this contract.
			_, exists = uuc.unusedHosts[piece.HostPubKey.String()]
			redundantPiece := uuc.pieceUsage[pieceIndex]
			if exists && !redundantPiece {
				uuc.pieceUsage[pieceIndex] = true
				uuc.piecesCompleted++
				delete(uuc.unusedHosts, piece.HostPubKey.String())
			} else if exists {
				// This host has a piece, but it is the same piece another
				// host has. We should still remove the host from the
				// unusedHosts since one host having multiple pieces of a
				// chunk might lead to unexpected issues. e.g. if a host
				// has multiple pieces and another host with redundant
				// pieces goes offline, we end up with false redundancy
				// reporting.
				delete(uuc.unusedHosts, piece.HostPubKey.String())
			}
		}
	}
	// Now that we have calculated the completed pieces for the chunk we can
	// calculate the health of the chunk to avoid a call to ChunkHealth
	uuc.health = 1 - (float64(uuc.piecesCompleted-uuc.minimumPieces) / float64(uuc.piecesNeeded-uuc.minimumPieces))
	return uuc, nil
}

// managedBuildUnfinishedChunks will pull all of the unfinished chunks out of a
// file.
//
// NOTE: each unfinishedUploadChunk needs its own SiaFileSetEntry. This is due
// to the SiaFiles being removed from memory. Since the renter does not keep the
// SiaFiles in memory the unfinishedUploadChunks need to close the SiaFile when
// they are done and so cannot share a SiaFileSetEntry as the first chunk to
// finish would then close the Entry and consequentially impact the remaining
// chunks.
func (r *Renter) managedBuildUnfinishedChunks(entry *siafile.SiaFileSetEntry, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) []*unfinishedUploadChunk {
	// If we don't have enough workers for the file, don't repair it right now.
	minPieces := entry.ErasureCode().MinPieces()
	r.staticWorkerPool.mu.RLock()
	workerPoolLen := len(r.staticWorkerPool.workers)
	r.staticWorkerPool.mu.RUnlock()
	if workerPoolLen < minPieces {
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
			// minimum redundancy. Mark all chunks as stuck
			r.log.Printf("WARN: allownace had insufficient hosts for chunk to reach minimum redundancy, have %v need %v for file %v", allowance.Hosts, minPieces, entry.SiaFilePath())
			if err := entry.SetAllStuck(true); err != nil {
				r.log.Println("WARN: unable to mark all chunks as stuck:", err)
			}
		}
		return nil
	}

	// Assemble chunk indexes, stuck Loop should only be adding stuck chunks and
	// the repair loop should only be adding unstuck chunks
	var chunkIndexes []uint64
	for i := uint64(0); i < entry.NumChunks(); i++ {
		stuck, err := entry.StuckChunkByIndex(i)
		if err != nil {
			r.log.Debugln("failed to get 'stuck' status of entry:", err)
			continue
		}
		if (target == targetStuckChunks) == stuck {
			chunkIndexes = append(chunkIndexes, i)
		}
	}

	// Sanity check that we have chunk indices to go through
	if len(chunkIndexes) == 0 {
		r.log.Println("WARN: no chunk indices gathered, can't add chunks to heap")
		return nil
	}

	// Build a map of host public keys. We assume that all entrys are the same.
	pks := make(map[string]types.SiaPublicKey)
	for _, pk := range entry.HostPublicKeys() {
		pks[string(pk.Key)] = pk
	}

	// Assemble the set of chunks.
	newUnfinishedChunks := make([]*unfinishedUploadChunk, 0, len(chunkIndexes))
	for _, index := range chunkIndexes {
		// Sanity check: fileUID should not be the empty value.
		if entry.UID() == "" {
			build.Critical("empty string for file UID")
		}

		// Create unfinishedUploadChunk
		chunk, err := r.managedBuildUnfinishedChunk(entry, uint64(index), hosts, pks, false, offline, goodForRenew)
		if err != nil {
			r.log.Debugln("Error when building an unfinished chunk:", err)
			continue
		}
		newUnfinishedChunks = append(newUnfinishedChunks, chunk)
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
		//
		// While a file could be on disk as long as !os.IsNotExist(err), for the
		// purposes of repairing a file is only considered on disk if it can be
		// accessed without error. If there is an error accessing the file then
		// it is likely that we can not read the file in which case it can not
		// be used for repair.
		_, err := os.Stat(chunk.fileEntry.LocalPath())
		onDisk := err == nil
		repairable := chunk.health <= 1 || onDisk
		needsRepair := chunk.health >= RepairThreshold

		// Add chunk to list of incompleteChunks if it is incomplete and
		// repairable or if we are targeting stuck chunks
		if needsRepair && (repairable || target == targetStuckChunks) {
			incompleteChunks = append(incompleteChunks, chunk)
			continue
		}

		// If a chunk is not able to be repaired, mark it as stuck.
		if !repairable {
			r.log.Println("Marking chunk", chunk.id, "as stuck due to not being repairable")
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

// managedAddChunksToHeap will add chunks to the upload heap one directory at a
// time until the directory heap is empty or the uploadheap is full. It does
// this by popping directories off the directory heap and adding the chunks from
// that directory to the upload heap. If the worst health directory found is
// sufficiently healthy then we return.
func (r *Renter) managedAddChunksToHeap(hosts map[string]struct{}) (map[modules.SiaPath]struct{}, error) {
	siaPaths := make(map[modules.SiaPath]struct{})
	prevHeapLen := r.uploadHeap.managedLen()
	// Loop until the upload heap has maxUploadHeapChunks in it or the directory
	// heap is empty
	for r.uploadHeap.managedLen() < maxUploadHeapChunks && r.directoryHeap.managedLen() > 0 {
		select {
		case <-r.tg.StopChan():
			return siaPaths, errors.New("renter shutdown before we could finish adding chunks to heap")
		default:
		}

		// Pop an explored directory off of the directory heap
		dir, err := r.managedNextExploredDirectory()
		if err != nil {
			r.log.Println("WARN: error getting explored directory:", err)
			// Reset the directory heap to try and help address the error
			r.directoryHeap.managedReset()
			return siaPaths, err
		}

		// Sanity Check if directory was returned
		if dir == nil {
			r.log.Debugln("no more chunks added to the upload heap because there are no more directories")
			return siaPaths, nil
		}

		// Grab health and siaPath of the directory
		dir.mu.Lock()
		dirHealth := dir.health
		dirSiaPath := dir.siaPath
		dir.mu.Unlock()

		// If the directory that was just popped is healthy then return
		if dirHealth < RepairThreshold {
			r.log.Debugln("no more chunks added to the upload heap because directory popped is healthy")
			return siaPaths, nil
		}

		// Add chunks from the directory to the uploadHeap.
		r.managedBuildChunkHeap(dirSiaPath, hosts, targetUnstuckChunks)

		// Check to see if we are still adding chunks
		heapLen := r.uploadHeap.managedLen()
		if heapLen == prevHeapLen {
			// No more chunks added to the uploadHeap from the worst health
			// directory. This means that the worse health chunks are already in
			// the heap or are currently being repaired, so return. This can be
			// the case in new uploads or repair loop iterations triggered from
			// bubble
			r.log.Debugln("no more chunks added to the upload heap")
			return siaPaths, nil
		}
		chunksAdded := heapLen - prevHeapLen
		prevHeapLen = heapLen

		// Since we added chunks from this directory, track the siaPath
		//
		// NOTE: we only want to remember each siaPath once which is why we use
		// a map. We Don't check if the siaPath is already in the map because
		// another thread could have added the directory back to the heap after
		// we just popped it off. This is the case for new uploads.
		siaPaths[dirSiaPath] = struct{}{}
		r.log.Println("Added", chunksAdded, "chunks from", dirSiaPath, "to the upload heap")
	}

	return siaPaths, nil
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
		unfinishedUploadChunks := r.managedBuildUnfinishedChunks(file, hosts, target, offline, goodForRenew)

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
//
// NOTE: the files submitted to this function should all be from the same
// directory
func (r *Renter) managedBuildAndPushChunks(files []*siafile.SiaFileSetEntry, hosts map[string]struct{}, target repairTarget, offline, goodForRenew map[string]bool) {
	// Sanity check that at least one file was provided
	if len(files) == 0 {
		build.Critical("managedBuildAndPushChunks called without providing any files")
		return
	}

	// Loop through the whole set of files and get a list of chunks and build a
	// temporary heap
	var unfinishedChunkHeap uploadChunkHeap
	var worstIgnoredHealth float64
	dirHeapHealth := r.directoryHeap.managedPeekHealth()
	for _, file := range files {
		// For normal repairs check if file is a worse health than the directory
		// heap
		fileHealth := file.Metadata().CachedHealth
		if fileHealth < dirHeapHealth && target == targetUnstuckChunks {
			worstIgnoredHealth = math.Max(worstIgnoredHealth, fileHealth)
			continue
		}

		// Build unfinished chunks from file and add them to the temp heap if
		// they are a worse health than the directory heap
		unfinishedUploadChunks := r.managedBuildUnfinishedChunks(file, hosts, target, offline, goodForRenew)
		for i := 0; i < len(unfinishedUploadChunks); i++ {
			chunk := unfinishedUploadChunks[i]
			// Check to see the chunk is already in the upload heap
			if r.uploadHeap.managedExists(chunk.id) {
				// Close the file entry
				err := chunk.fileEntry.Close()
				if err != nil {
					r.log.Println("WARN: unable to close file:", err)
				}
				// Since the chunk is already in the heap we do not need to
				// track the health of the chunk
				continue
			}

			// For normal repairs check if chunk has a worse health than the
			// directory heap
			if chunk.health < dirHeapHealth && target == targetUnstuckChunks {
				// Track the health
				worstIgnoredHealth = math.Max(worstIgnoredHealth, chunk.health)
				// Close the file entry
				err := chunk.fileEntry.Close()
				if err != nil {
					r.log.Println("WARN: unable to close file:", err)
				}
				continue
			}

			// Add chunk to temp heap
			heap.Push(&unfinishedChunkHeap, chunk)

			// Check if temp heap is growing too large. We want to restrict it
			// to twice the size of the max upload heap size. This restriction
			// should be applied to all repairs to prevent excessive memory
			// usage.
			if len(unfinishedChunkHeap) < maxUploadHeapChunks*2 {
				continue
			}

			// Pop of the worst half of the heap
			var chunksToKeep []*unfinishedUploadChunk
			for len(unfinishedChunkHeap) > maxUploadHeapChunks {
				chunksToKeep = append(chunksToKeep, heap.Pop(&unfinishedChunkHeap).(*unfinishedUploadChunk))
			}

			// Check health of next chunk
			chunk = heap.Pop(&unfinishedChunkHeap).(*unfinishedUploadChunk)
			worstIgnoredHealth = math.Max(worstIgnoredHealth, chunk.health)
			// Close the file entry
			err := chunk.fileEntry.Close()
			if err != nil {
				r.log.Println("WARN: unable to close file:", err)
			}

			// Reset temp heap to release memory
			err = unfinishedChunkHeap.reset()
			if err != nil {
				r.log.Println("WARN: error resetting the temporary upload heap:", err)
			}

			// Add worst chunks back to heap
			for _, chunk := range chunksToKeep {
				heap.Push(&unfinishedChunkHeap, chunk)
			}

			// Make sure chunksToKeep is zeroed out in memory
			chunksToKeep = []*unfinishedUploadChunk{}
		}
	}

	// We now have a temporary heap of the worst chunks in the directory that
	// are also worse than any other chunk in the directory heap. Now we try and
	// add as many chunks as we can to the uploadHeap
	for len(unfinishedChunkHeap) > 0 && (r.uploadHeap.managedLen() < maxUploadHeapChunks || target == targetBackupChunks) {
		// Add chunk to the uploadHeap
		chunk := heap.Pop(&unfinishedChunkHeap).(*unfinishedUploadChunk)
		if !r.uploadHeap.managedPush(chunk) {
			// We don't track the health of this chunk since the only reason it
			// wouldn't be added to the heap is if it is already in the heap or
			// is currently being repaired. Close the file.
			err := chunk.fileEntry.Close()
			if err != nil {
				r.log.Println("WARN: unable to close file:", err)
			}
		}
	}

	// Check if there are still chunks left in the temp heap. If so check the
	// health of the next chunk
	if len(unfinishedChunkHeap) > 0 {
		chunk := heap.Pop(&unfinishedChunkHeap).(*unfinishedUploadChunk)
		worstIgnoredHealth = math.Max(worstIgnoredHealth, chunk.health)
		// Close the chunk's file
		err := chunk.fileEntry.Close()
		if err != nil {
			r.log.Println("WARN: unable to close file:", err)
		}
	}

	// We are done with the temporary heap so reset it to help release the
	// memory
	err := unfinishedChunkHeap.reset()
	if err != nil {
		r.log.Println("WARN: error resetting the temporary upload heap:", err)
	}

	// Check if we were adding backup chunks, if so return here as backups are
	// not added to the directory heap
	if target == targetBackupChunks {
		return
	}

	// Check if we should add the directory back to the directory heap
	if worstIgnoredHealth < RepairThreshold {
		return
	}

	// All files submitted are from the same directory so use the first one to
	// get the directory siapath
	dirSiaPath, err := r.staticFileSet.SiaPath(files[0]).Dir()
	if err != nil {
		r.log.Println("WARN: unable to get directory SiaPath and add directory back to directory heap:", err)
		return
	}

	// Since directory is being added back as explored we only need to set the
	// health as that is what will be used for sorting in the directory heap.
	//
	// The aggregate health is set to 'worstIgnoredHealth' as well. In the event
	// that the directory gets added as unexplored because another copy of the
	// unexplored directory exists on the directory heap, we need to make sure
	// that the worst known health is represented in the aggregate value.
	d := &directory{
		aggregateHealth: worstIgnoredHealth,
		health:          worstIgnoredHealth,
		explored:        true,
		siaPath:         dirSiaPath,
	}
	// Add the directory to the heap. If there is a conflict because the
	// directory is already in the heap (for example, added by another thread or
	// process), then the worst of the values between this dir and the one
	// that's already in the dir will be used, to ensure that the repair loop
	// will prioritize all bad value files.
	r.directoryHeap.managedPush(d)
}

// managedBuildChunkHeap will iterate through all of the files in the renter and
// construct a chunk heap.
//
// TODO: accept an input to indicate how much room is in the heap
//
// TODO: Explore whether there is a way to perform the task below without
// opening a full file entry for each file in the directory.
func (r *Renter) managedBuildChunkHeap(dirSiaPath modules.SiaPath, hosts map[string]struct{}, target repairTarget) {
	// Get Directory files
	var fileinfos []os.FileInfo
	var err error
	if target == targetBackupChunks {
		fileinfos, err = ioutil.ReadDir(dirSiaPath.SiaDirSysPath(r.staticBackupsDir))
	} else {
		fileinfos, err = ioutil.ReadDir(dirSiaPath.SiaDirSysPath(r.staticFilesDir))
	}
	if err != nil {
		r.log.Println("WARN: could not read directory:", err)
		return
	}
	// Build files from fileinfos
	var files []*siafile.SiaFileSetEntry
	for _, fi := range fileinfos {
		// skip sub directories and non siafiles
		ext := filepath.Ext(fi.Name())
		if fi.IsDir() || ext != modules.SiaFileExtension {
			continue
		}

		// Open SiaFile
		siaPath, err := dirSiaPath.Join(strings.TrimSuffix(fi.Name(), ext))
		if err != nil {
			r.log.Println("WARN: could not create siaPath:", err)
			continue
		}
		var file *siafile.SiaFileSetEntry
		if target == targetBackupChunks {
			file, err = r.staticBackupFileSet.Open(siaPath)
		} else {
			file, err = r.staticFileSet.Open(siaPath)
		}
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
		// For normal repairs, ignore files that don't have any unstuck chunks
		// or are healthy.
		//
		// We can used the cached value of health because it is updated during
		// bubble. Since the repair loop operates off of the metadata
		// information updated by bubble this cached health is accurate enough
		// to use in order to determine if a file has any chunks that need
		// repair
		ignore := file.NumChunks() == file.NumStuckChunks() || file.Metadata().CachedHealth < RepairThreshold
		if target == targetUnstuckChunks && ignore {
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

	// If there are more files than there is room in the heap, sort the files by
	// health and only use the required number of files to build the heap. In
	// the absolute worst case, each file will be only contributing one chunk to
	// the heap, so this shortcut will not be missing any important chunks. This
	// shortcut will also not be used for directories that have fewer than
	// 'maxUploadHeapChunks' files in them, minimzing the impact of this code in
	// the typical case.
	//
	// This check only applies to normal repairs. Stuck repairs have their own
	// way of managing the number of chunks added to the heap and backup chunks
	// should always be added.
	//
	// v1.4.1 Benchmark: on a computer with an SSD, the time to sort 6,000 files
	// is less than 50 milliseconds, while the time to process 250 files with 40
	// chunks each using 'managedBuildAndPushChunks' is several seconds. Even in
	// the worst case, where we are sorting 251 files with 1 chunk each, there
	// is not much slowdown compared to skipping the sort, because the sort is
	// so fast.
	if len(files) > maxUploadHeapChunks && target == targetUnstuckChunks {
		// Sort so that the highest health chunks will be first in the array.
		// Higher health values equal worse health for the file, and we want to
		// focus on the worst files.
		sort.Slice(files, func(i, j int) bool {
			return files[i].Metadata().CachedHealth > files[j].Metadata().CachedHealth
		})
		for i := maxUploadHeapChunks; i < len(files); i++ {
			err := files[i].Close()
			if err != nil {
				r.log.Println("WARN: Could not close file:", err)
			}
		}
		files = files[:maxUploadHeapChunks]
	}

	// Build the unfinished upload chunks and add them to the upload heap
	offline, goodForRenew, _ := r.managedContractUtilityMaps()
	switch target {
	case targetBackupChunks:
		r.log.Debugln("Attempting to add backup chunks to heap")
		r.managedBuildAndPushChunks(files, hosts, target, offline, goodForRenew)
	case targetStuckChunks:
		r.log.Debugln("Attempting to add stuck chunk to heap")
		r.managedBuildAndPushRandomChunk(files, maxStuckChunksInHeap, hosts, target, offline, goodForRenew)
	case targetUnstuckChunks:
		r.log.Debugln("Attempting to add chunks to heap")
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
//
// TODO: This function can be removed entirely if the worker pool is made to
// keep a list of hosts. Then instead of passing around the hosts as a parameter
// the cached value in the worker pool can be used instead. Using the cached
// value in the worker pool is more accurate anyway because the hosts field will
// match the set of workers that we have. Doing it the current way means there
// can be drift between the set of workers and the set of hosts we are using to
// build out the chunk heap.
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
	r.staticWorkerPool.callUpdate()
	return hosts
}

// managedRepairLoop works through the uploadheap repairing chunks. The repair
// loop will continue until the renter stops, there are no more chunks, or the
// number of chunks in the uploadheap has dropped below the minUploadHeapSize
func (r *Renter) managedRepairLoop(hosts map[string]struct{}) error {
	// smallRepair indicates whether or not the repair loop should process all
	// of the chunks in the heap instead of just processing down to the minimum
	// heap size. We want to process all of the chunks if the rest of the
	// directory heap is in good health and there are no more chunks that could
	// be added to the heap.
	smallRepair := r.directoryHeap.managedPeekHealth() < RepairThreshold

	// Limit the amount of time spent in each iteration of the repair loop so
	// that changes to the directory heap take effect sooner rather than later.
	repairBreakTime := time.Now().Add(maxRepairLoopTime)

	// Work through the heap repairing chunks until heap is empty for
	// smallRepairs or heap drops below minUploadHeapSize for larger repairs, or
	// until the total amount of time spent in one repair iteration has elapsed.
	for r.uploadHeap.managedLen() >= minUploadHeapSize || smallRepair || time.Now().After(repairBreakTime) {
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

		// Check if there is work by trying to pop off the next chunk from the
		// heap.
		nextChunk := r.uploadHeap.managedPop()
		if nextChunk == nil {
			// The heap is empty so reset it to free memory and return.
			r.uploadHeap.managedReset()
			return nil
		}

		// Make sure we have enough workers for this chunk to reach minimum
		// redundancy.
		r.staticWorkerPool.mu.RLock()
		availableWorkers := len(r.staticWorkerPool.workers)
		r.staticWorkerPool.mu.RUnlock()
		if availableWorkers < nextChunk.minimumPieces {
			// If the chunk is not stuck, check whether there are enough hosts
			// in the allowance to support the chunk.
			if !nextChunk.stuck {
				// There are not enough available workers for the chunk to reach
				// minimum redundancy. Check if the allowance has enough hosts
				// for the chunk to reach minimum redundancy
				allowance := r.hostContractor.Allowance()
				if allowance.Hosts < uint64(nextChunk.minimumPieces) {
					// There are not enough hosts in the allowance for this
					// chunk to reach minimum redundancy. Log an error, set the
					// chunk as stuck, and close the file
					r.log.Printf("WARN: allownace had insufficient hosts for chunk to reach minimum redundancy, have %v need %v for chunk %v", allowance.Hosts, nextChunk.minimumPieces, nextChunk.id)
					err := nextChunk.fileEntry.SetStuck(nextChunk.index, true)
					if err != nil {
						r.log.Debugln("WARN: unable to mark chunk as stuck:", err, nextChunk.id)
					}
				}
			}

			// There are enough hosts set in the allowance so this is a
			// temporary issue with available workers, just ignore the chunk
			// for now and close the file
			err := nextChunk.fileEntry.Close()
			if err != nil {
				r.log.Debugln("WARN: unable to close file:", err, nextChunk.fileEntry.SiaFilePath())
			}
			// Remove the chunk from the repairingChunks map
			r.uploadHeap.managedMarkRepairDone(nextChunk.id)
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
			// Remove the chunk from the repairingChunks map
			r.uploadHeap.managedMarkRepairDone(nextChunk.id)
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

	// Perpetual loop to scan for more files and add chunks to the uploadheap.
	// The loop assumes that the heap has already been initialized (either at
	// startup, or after sleeping) and does checks to see whether there is any
	// work required. If there is not any work required, the loop will sleep
	// until woken up. If there is work required, the loop will begin to process
	// the chunks and directories in the repair heaps.
	//
	// After 'repairLoopResetFrequency', the repair loop will be reset. This
	// adds a layer of robustness in case the repair loop gets stuck or can't
	// work through the full heap quickly because the user keeps uploading new
	// files and keeping a minimum number of chunks in the repair heap.
	resetTime := time.Now().Add(repairLoopResetFrequency)
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
		// Refresh the worker set.
		hosts := r.managedRefreshHostsAndWorkers()

		// If enough time has elapsed to trigger a directory reset, reset the
		// directory.
		if time.Now().After(resetTime) {
			resetTime = time.Now().Add(repairLoopResetFrequency)
			r.directoryHeap.managedReset()
			err = r.managedPushUnexploredDirectory(modules.RootSiaPath())
			if err != nil {
				r.log.Println("WARN: error re-initializing the directory heap:", err)
			}
		}

		// Add any chunks from the backup heap that need to be repaired. This
		// needs to be handled separately because currently the filesystem for
		// storing system files and chunks such as those related to snapshot
		// backups is different from the siafileset that stores non-system files
		// and chunks.
		heapLen := r.uploadHeap.managedLen()
		r.managedBuildChunkHeap(modules.RootSiaPath(), hosts, targetBackupChunks)
		numBackupchunks := r.uploadHeap.managedLen() - heapLen
		if numBackupchunks > 0 {
			r.log.Println("Added", numBackupchunks, "backup chunks to the upload heap")
		}

		// Check if there is work to do. If the filesystem is healthy and the
		// heap is empty, there is no work to do and the thread should block
		// until there is work to do.
		if r.uploadHeap.managedLen() == 0 && r.directoryHeap.managedPeekHealth() < RepairThreshold {
			// TODO: This has a tiny window where it might be dumping out chunks
			// that need health, if the upload call is appending to the
			// directory heap because there is a new upload.
			//
			// I believe that a good fix for this would be to change the upload
			// heap so that it performs a rapid bubble before trying to insert
			// the chunks into the heap. Then, even if a reset is triggered,
			// because a rapid bubble has already completed updating the health
			// of the root dir, it will be considered fairly.
			r.directoryHeap.managedReset()

			// If the file system is healthy then block until there is a new
			// upload or there is a repair that is needed.
			select {
			case <-r.uploadHeap.newUploads:
				r.log.Debugln("repair loop triggered by new upload channel")
			case <-r.uploadHeap.repairNeeded:
				r.log.Debugln("repair loop triggered by repair needed channel")
			case <-r.tg.StopChan():
				return
			}

			err = r.managedPushUnexploredDirectory(modules.RootSiaPath())
			if err != nil {
				// If there is an error initializing the directory heap log
				// the error. We don't want to sleep here as we were trigger
				// to repair chunks so we don't want to delay the repair if
				// there are chunks in the upload heap already.
				r.log.Println("WARN: error re-initializing the directory heap:", err)
			}

			// Continue here to force the code to re-check for backups, to
			// re-block until it's online, and to refresh the worker pool.
			continue
		}

		// Add chunks to heap.
		dirSiaPaths := make(map[modules.SiaPath]struct{})
		dirSiaPaths, err = r.managedAddChunksToHeap(hosts)
		if err != nil {
			// Log the error but don't sleep as there are potentially chunks in
			// the heap from new uploads. If the heap is empty the next check
			// will catch that and handle it as an error
			r.log.Debugln("WARN: error adding chunks to the heap:", err)
		}

		// There are benign edge cases where the heap will be empty after chunks
		// have been added. For example, if a chunk has gotten more healthy
		// since the last health check due to one of its hosts coming back
		// online. In these cases, the best course of action is to proceed with
		// the repair and move on to the next directories in the directory heap.
		// The repair loop will return immediately if it is given little or no
		// work but it can see that there is more work that it could be given.

		r.log.Debugln("Executing an upload and repair cycle, uploadHeap has", r.uploadHeap.managedLen(), "chunks in it")
		err = r.managedRepairLoop(hosts)
		if err != nil {
			// If there was an error with the repair loop sleep for a little bit
			// and then try again. Here we do not skip to the next iteration as
			// we want to call bubble on the impacted directories
			r.log.Println("WARN: there was an error in the repair loop:", err)
			select {
			case <-time.After(uploadAndRepairErrorSleepDuration):
			case <-r.tg.StopChan():
				return
			}
		}

		// Call threadedBubbleMetadata to update the filesystem.
		for dirSiaPath := range dirSiaPaths {
			// We call bubble in a go routine so that it is not a bottle neck
			// for the repair loop iterations. This however can lead to some
			// additional unneeded cycles of the repair loop as a result of when
			// these bubbles reach root. This cycles however will be handled and
			// can be seen in the logs.
			go r.threadedBubbleMetadata(dirSiaPath)
		}
	}
}
