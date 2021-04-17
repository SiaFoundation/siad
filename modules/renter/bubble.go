package renter

import (
	"container/list"
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
)

// Bubble is the process of updating the filesystem metadata for the renter. It
// is called bubble because when a directory's metadata is updated, a call to
// update the parent directory will be made. This process continues until the
// root directory is reached. This results in any changes in metadata being
// "bubbled" to the top so that the root directory's metadata reflects the
// status of the entire filesystem.

// bubbleStatus indicates the status of a bubble being executed on a
// directory
type bubbleStatus int

// bubbleError, bubbleQueued, bubbleActive, and bubblePending are the constants
// used to determine the status of a bubble being executed on a directory
const (
	bubbleError bubbleStatus = iota
	bubbleQueued
	bubbleActive
	bubblePending
)

type (
	// bubbleQueue is a queue of bubble updates
	bubbleQueue struct {
		*list.List
	}

	// bubbleScheduler contains information needed to schedule bubbles which update
	// the metadata of the renter's filesystem. The bubbleScheduler is responsible
	// for managing the number of concurrent bubble updates as well as ensuring
	// that all bubble updates are processed.
	bubbleScheduler struct {
		// bubbleNeeded is a channel used to signal the bubbleScheduler that a bubble
		// is needed
		bubbleNeeded chan struct{}

		// bubbleUpdates is a map of the requested bubble updates
		bubbleUpdates map[modules.SiaPath]*bubbleUpdate

		// fifo is a First In Fist Out queue of bubble updates
		fifo *bubbleQueue

		// Utilities
		mu           sync.Mutex
		staticRenter *Renter
	}

	// bubbleUpdate contains the information about a bubble update
	bubbleUpdate struct {
		// complete is a channel used to signal if a bubble has been completed on
		// the directory. This is used so a caller can block until the bubble has
		// executed at least once. Since bubble updates can be added back to the
		// queue this channel is reused.
		complete chan struct{}

		// staticSiaPath of the directory that should be bubbled
		staticSiaPath modules.SiaPath

		// Current status of the bubble
		status bubbleStatus
	}
)

// newBubbleQueue returns an initialized bubbleQueue
func newBubbleQueue() *bubbleQueue {
	return &bubbleQueue{
		List: list.New(),
	}
}

// newBubbleScheduler returns an initialized bubbleScheduler
func newBubbleScheduler(r *Renter) *bubbleScheduler {
	return &bubbleScheduler{
		bubbleNeeded:  make(chan struct{}, 1),
		bubbleUpdates: make(map[modules.SiaPath]*bubbleUpdate),
		fifo:          newBubbleQueue(),

		staticRenter: r,
	}
}

// Pop removes the first element from the queue
func (bq *bubbleQueue) Pop() *bubbleUpdate {
	bu := bq.Front()
	if bu == nil {
		return nil
	}
	return bq.List.Remove(bu).(*bubbleUpdate)
}

// Push adds an element to the back of the queue
func (bq *bubbleQueue) Push(bu *bubbleUpdate) {
	_ = bq.List.PushBack(bu)
}

// callQueueBubble adds a bubble update request to the bubbleScheduler.
func (bs *bubbleScheduler) callQueueBubble(siaPath modules.SiaPath) chan struct{} {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Since there is a request for a bubble, make sure that after we process the
	// request we trigger the bubbleNeeded channel
	defer func() {
		select {
		case bs.bubbleNeeded <- struct{}{}:
		default:
		}
	}()

	// Check for bubble in bubbleUpdate map
	bu, ok := bs.bubbleUpdates[siaPath]
	if !ok {
		// No bubble update for siaPath. Add to the map and queue with bubbleStatus
		// bubbleQueued
		bu = &bubbleUpdate{
			complete:      make(chan struct{}),
			staticSiaPath: siaPath,
			status:        bubbleQueued,
		}
		bs.bubbleUpdates[siaPath] = bu
		bs.fifo.Push(bu)
		return bu.complete
	}

	// There is already a bubble update in the map, check the status
	switch bu.status {
	case bubbleQueued:
		// The update is currently queued so this new request will be satisfied when
		// the current update gets executed
	case bubbleActive:
		// There is an active bubble update in process. This means we should update
		// the status to pending so that another bubble update will be queued when
		// the current one completes.
		bu.status = bubblePending
	case bubblePending:
		// There is an active bubble update in process and another thread has
		// already requested another bubble update.
	default:
		str := fmt.Sprintf("bubbleError status for '%v' found in callQueueBubble", siaPath)
		build.Critical(str)
	}
	return bu.complete
}

// callThreadedProcessBubbleUpdates is a background loop that processes the
// queued bubble update requests.
func (bs *bubbleScheduler) callThreadedProcessBubbleUpdates() {
	err := bs.staticRenter.tg.Add()
	if err != nil {
		return
	}
	defer bs.staticRenter.tg.Done()

	// Define bubble worker
	bubbleWorker := func(siaPathChan chan modules.SiaPath) {
		for siaPath := range siaPathChan {
			// Perform the bubble update
			err := bs.managedPerformBubbleUpdate(siaPath)
			if err != nil {
				bs.staticRenter.log.Printf("WARN: error performing bubble on '%v': %v", siaPath, err)
			}

			// Complete the bubble
			bs.managedCompleteBubbleUpdate(siaPath)

			// Queue a bubble on the parent directory
			err = bs.managedQueueParent(siaPath)
			if err != nil {
				bs.staticRenter.log.Printf("WARN: error queuing bubble for parent directory on '%v': %v", siaPath, err)
			}
		}
	}
	var wg sync.WaitGroup

	for {
		// Block until a bubble is needed
		select {
		case <-bs.staticRenter.tg.StopChan():
			return
		case <-bs.bubbleNeeded:
		}

		// Launch a group of bubble workers
		bubbleChan := make(chan modules.SiaPath, numBubbleWorkerThreads)
		for i := 0; i < numBubbleWorkerThreads; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				bubbleWorker(bubbleChan)
			}()
		}

		// Send the queued bubbles to the workers
		bu := bs.managedPop()
		for bu != nil {
			// Send the siaPath to the workers via the bubbleChan
			select {
			case <-bs.staticRenter.tg.StopChan():
				close(bubbleChan)
				wg.Wait()
				return
			case bubbleChan <- bu.staticSiaPath:
			}
			bu = bs.managedPop()
		}

		// Close the chan and wait for the worker threads to close
		close(bubbleChan)
		wg.Wait()
	}
}

// managedCompleteBubbleUpdate will complete the bubble update and update the
// status and the bubble map accordingly.
func (bs *bubbleScheduler) managedCompleteBubbleUpdate(siaPath modules.SiaPath) {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Grab the bubble update from the map
	bu, ok := bs.bubbleUpdates[siaPath]
	if !ok {
		str := fmt.Sprintf("bubble update for '%v' not found in map when complete is called", siaPath)
		build.Critical(str)
		return
	}

	// Signal that a bubble has been completed to release any blocking calls.
	close(bu.complete)

	// Complete based on the status of the update
	switch bu.status {
	case bubbleQueued:
		// bubbleQueue status was found, remove from map to try and clean up the error
		str := fmt.Sprintf("bubbleQueue status for '%v' found during complete call", siaPath)
		build.Critical(str)
		delete(bs.bubbleUpdates, siaPath)
		return
	case bubbleActive:
		// If the status is still bubbleActive it means no other bubble requests
		// were made while the bubble was in progress. The bubble update is complete
		// so we can remove it from the map.
		delete(bs.bubbleUpdates, siaPath)
		return
	case bubblePending:
		// If the status is bubblePending it means a bubble request was made while
		// the current bubble was in progress. In this case we add the update back
		// to the queue with a status of bubbleQueued and a new complete chan.
		bu.status = bubbleQueued
		bu.complete = make(chan struct{})
		bs.fifo.Push(bu)
		return
	default:
		// Error was found, remove from map to try and clean up the error
		str := fmt.Sprintf("bubbleError status for '%v' found during complete call", siaPath)
		build.Critical(str)
		delete(bs.bubbleUpdates, siaPath)
		return
	}
}

// managedPerformBubbleUpdate performs the bubble update by calculating the
// metadata for the directory and saving the updates to disk. This update
// involved updating the metadata for the files in the directory as well.
func (bs *bubbleScheduler) managedPerformBubbleUpdate(siaPath modules.SiaPath) (err error) {
	// Grab the renter for ease
	r := bs.staticRenter

	// Update the File metadatas in the directory.
	offlineMap, goodForRenewMap, contracts, used := r.callRenterContractsAndUtilities()
	err = r.managedUpdateFileMetadatasParams(siaPath, offlineMap, goodForRenewMap, contracts, used)
	if err != nil {
		e := fmt.Sprintf("unable to update the file metadatas for directory '%v'", siaPath.String())
		return errors.AddContext(err, e)
	}

	// Calculate the new metadata values of the directory
	metadata, err := r.callCalculateDirectoryMetadata(siaPath)
	if err != nil {
		e := fmt.Sprintf("could not calculate the metadata of directory '%v'", siaPath.String())
		return errors.AddContext(err, e)
	}

	// Update directory metadata with the health information. Don't return here
	// to avoid skipping the repairNeeded and stuckChunkFound signals.
	siaDir, err := r.staticFileSystem.OpenSiaDir(siaPath)
	if err != nil {
		e := fmt.Sprintf("could not open directory %v", siaPath.String())
		err = errors.AddContext(err, e)
	} else {
		defer func() {
			err = errors.Compose(err, siaDir.Close())
		}()
		err = siaDir.UpdateBubbledMetadata(metadata)
		if err != nil {
			e := fmt.Sprintf("could not update the metadata of the directory %v", siaPath.String())
			err = errors.AddContext(err, e)
		}
	}

	// If we are at the root directory then check if any files were found in
	// need of repair or and stuck chunks and trigger the appropriate repair
	// loop. This is only done at the root directory as the repair and stuck
	// loops start at the root directory so there is no point triggering them
	// until the root directory is updated
	if siaPath.IsRoot() {
		if modules.NeedsRepair(metadata.AggregateHealth) {
			select {
			case r.uploadHeap.repairNeeded <- struct{}{}:
			default:
			}
		}
		if metadata.AggregateNumStuckChunks > 0 {
			select {
			case r.uploadHeap.stuckChunkFound <- struct{}{}:
			default:
			}
		}
	}
	return err
}

// managedPop pops the next bubble update off of the fifo queue and updates the
// bubble status.
func (bs *bubbleScheduler) managedPop() *bubbleUpdate {
	bs.mu.Lock()
	defer bs.mu.Unlock()

	// Grab the next bubble update
	bu := bs.fifo.Pop()
	if bu == nil {
		return nil
	}

	// Sanity Checks
	if bu.status != bubbleQueued {
		build.Critical("bubble update popped from bubbleQueue with a non queued status")
	}
	_, ok := bs.bubbleUpdates[bu.staticSiaPath]
	if !ok {
		build.Critical("bubble update popped from queue not found in bubble update map")
	}

	// Update the status and return
	bu.status = bubbleActive
	return bu
}

// managedQueueParent will queue a bubble for the parent directory.
func (bs *bubbleScheduler) managedQueueParent(siaPath modules.SiaPath) error {
	// If we are at the root directory there is nothing to do.
	if siaPath.IsRoot() {
		return nil
	}

	// Grab the parent directory
	parentDir, err := siaPath.Dir()
	if err != nil {
		return errors.AddContext(err, "failed to get parent dir")
	}

	// Queue a bubble to bubble the directory, ignore the return channel as we
	// do not want to block on this update.
	_ = bs.callQueueBubble(parentDir)
	return nil
}

// BubbleMetadata will queue a bubble update for the directory. A bubble update
// includes calculating the updated values of a directory's metadata, updating
// the siadir metadata on disk, and then queuing a bubble update for the parent
// directory. This process will continue until the root directory is reached.
//
// This method is only blocking for the queuing of the bubble, or the
// preparation of the subtree if recursive is true.
//
// If the recursive boolean is supplied, all sub directories will be queued.
//
// If the force boolean is supplied, the LastHealthCheckTime of the directories
// will be ignored so all directories will be considered.
func (r *Renter) BubbleMetadata(siaPath modules.SiaPath, force, recursive bool) error {
	if err := r.tg.Add(); err != nil {
		return err
	}
	defer r.tg.Done()

	// If this is not a recursive call then call bubble
	if !recursive {
		// Queue a bubble to bubble the directory, ignore the return channel as we
		// do not want to block on this update.
		_ = r.staticBubbleScheduler.callQueueBubble(siaPath)
		return nil
	}

	// Prepare the subtree for bubble
	urp, err := r.callPrepareForBubble(siaPath, force)
	if err != nil {
		return errors.AddContext(err, "unable to prepare subtree for bubble")
	}
	// Call bubble in a non blocking manner
	return urp.callRefreshAll()
}
