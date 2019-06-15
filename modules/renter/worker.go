package renter

import (
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// A worker listens for work on a certain host.
//
// The mutex of the worker only protects the 'unprocessedChunks' and the
// 'standbyChunks' fields of the worker. The rest of the fields are only
// interacted with exclusively by the primary worker thread, and only one of
// those ever exists at a time.
//
// The workers have a concept of 'cooldown' for uploads and downloads. If a
// download or upload operation fails, the assumption is that future attempts
// are also likely to fail, because whatever condition resulted in the failure
// will still be present until some time has passed. Without any cooldowns,
// uploading and downloading with flaky hosts in the worker sets has
// substantially reduced overall performance and throughput.
type worker struct {
	// The hostPubKey also serves as an id for the worker, as there is only one
	// worker per host.
	hostPubKey types.SiaPublicKey

	// Download variables that are not protected by a mutex, but also do not
	// need to be protected by a mutex, as they are only accessed by the master
	// thread for the worker.
	//
	// The 'owned' prefix here indicates that only the master thread for the
	// object (in this case, 'threadedWorkLoop') is allowed to access these
	// variables. Because only that thread is allowed to access the variables,
	// that thread is able to access these variables without a mutex.
	ownedDownloadConsecutiveFailures int       // How many failures in a row?
	ownedDownloadRecentFailure       time.Time // How recent was the last failure?

	// Download variables related to queuing work. They have a separate mutex to
	// minimize lock contention.
	downloadChan       chan struct{}              // Notifications of new work. Takes priority over uploads.
	downloadChunks     []*unfinishedDownloadChunk // Yet unprocessed work items.
	downloadMu         sync.Mutex
	downloadTerminated bool // Has downloading been terminated for this worker?

	// Upload variables.
	unprocessedChunks         []*unfinishedUploadChunk // Yet unprocessed work items.
	uploadChan                chan struct{}            // Notifications of new work.
	uploadConsecutiveFailures int                      // How many times in a row uploading has failed.
	uploadRecentFailure       time.Time                // How recent was the last failure?
	uploadRecentFailureErr    error                    // What was the reason for the last failure?
	uploadTerminated          bool                     // Have we stopped uploading?

	// Utilities.
	//
	// The mutex is only needed when interacting with 'downloadChunks' and
	// 'unprocessedChunks', as everything else is only accessed from the single
	// master thread.
	killChan chan struct{} // Worker will shut down if a signal is sent down this channel.
	mu       sync.Mutex
	renter   *Renter
}

// workerPool is the collection of workers that the renter can use for
// uploading, downloading, and other tasks related to communicating with the
// host. There is one worker per host that the renter has a contract with. This
// includes hosts that have been disabled or otherwise been marked as
// !GoodForRenew or !GoodForUpload. We keep all of these workers so that they
// can be used in emergencies in the event that there seems to be no other way
// to recover data.
//
// TODO: Currently the repair loop does a lot of fetching and passing of host
// maps and offline maps and goodforrenew maps. All of those objects should be
// cached in the worker pool, which will both improve performance and reduce the
// calling complexity of the functions that currently need to pass this
// information around.
type workerPool struct {
	workers map[string]*worker // The string is the host's public key.
	mu      sync.RWMutex
	renter  *Renter
}

// threadedWorkLoop repeatedly issues work to a worker, stopping when the worker
// is killed or when the thread group is closed.
func (w *worker) threadedWorkLoop() {
	defer w.managedKillUploading()
	defer w.managedKillDownloading()

	for {
		// Perform one step of processing download work.
		downloadChunk := w.managedNextDownloadChunk()
		if downloadChunk != nil {
			// managedDownload will handle removing the worker internally. If
			// the chunk is dropped from the worker, the worker will be removed
			// from the chunk. If the worker executes a download (success or
			// failure), the worker will be removed from the chunk. If the
			// worker is put on standby, it will not be removed from the chunk.
			w.managedDownload(downloadChunk)
			continue
		}

		// Perform one step of processing upload work.
		chunk, pieceIndex := w.managedNextUploadChunk()
		if chunk != nil {
			w.managedUpload(chunk, pieceIndex)
			continue
		}

		// Block until new work is received via the upload or download channels,
		// or until a kill or stop signal is received.
		select {
		case <-w.downloadChan:
			continue
		case <-w.uploadChan:
			continue
		case <-w.killChan:
			return
		case <-w.renter.tg.StopChan():
			return
		}
	}
}

// managedUpdate will grab the set of contracts from the contractor and update
// the worker pool to match.
func (wp *workerPool) managedUpdate() {
	contractSlice := wp.renter.hostContractor.Contracts()
	contractMap := make(map[string]modules.RenterContract, len(contractSlice))
	for _, contract := range contractSlice {
		contractMap[contract.HostPublicKey.String()] = contract
	}

	// Add a worker for any contract that does not already have a worker.
	for id, contract := range contractMap {
		wp.mu.Lock()
		_, exists := wp.workers[id]
		if !exists {
			w := &worker{
				hostPubKey: contract.HostPublicKey,

				downloadChan: make(chan struct{}, 1),
				killChan:     make(chan struct{}),
				uploadChan:   make(chan struct{}, 1),

				renter: wp.renter,
			}
			wp.workers[id] = w
			if err := wp.renter.tg.Add(); err != nil {
				// Renter shutdown is happening, abort the loop to create more
				// workers.
				wp.mu.Unlock()
				break
			}
			go func() {
				defer wp.renter.tg.Done()
				w.threadedWorkLoop()
			}()
		}
		wp.mu.Unlock()
	}

	// Remove a worker for any worker that is not in the set of new contracts.
	totalCoolDown := 0
	wp.mu.Lock()
	for id, worker := range wp.workers {
		select {
		case <-wp.renter.tg.StopChan():
			// Release the lock and return to prevent error of trying to close
			// the worker channel after a shutdown
			wp.mu.Unlock()
			return
		default:
		}
		contract, exists := contractMap[id]
		if !exists {
			delete(wp.workers, id)
			close(worker.killChan)
		}
		worker.mu.Lock()
		onCoolDown, coolDownTime := worker.onUploadCooldown()
		if onCoolDown {
			totalCoolDown++
		}
		wp.renter.log.Debugf("Worker %v is GoodForUpload %v for contract %v\n    and is on uploadCooldown %v for %v because of %v", worker.hostPubKey, contract.Utility.GoodForUpload, contract.ID, onCoolDown, coolDownTime, worker.uploadRecentFailureErr)
		worker.mu.Unlock()
	}
	wp.renter.log.Debugf("Refreshed Worker Pool has %v total workers and %v are on cooldown", len(wp.workers), totalCoolDown)
	wp.mu.Unlock()
}

// newWorkerPool will initialize and return a worker pool.
func (r *Renter) newWorkerPool() *workerPool {
	wp := &workerPool{
		workers: make(map[string]*worker),
		renter:  r,
	}
	wp.renter.tg.OnStop(func() error {
		wp.mu.RLock()
		for _, w := range wp.workers {
			close(w.killChan)
		}
		wp.mu.RUnlock()
		return nil
	})
	wp.managedUpdate()
	return wp
}
