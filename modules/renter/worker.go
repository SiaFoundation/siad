package renter

// worker.go defines a worker with a work loop. Each worker is connected to a
// single host, and the work loop will listen for jobs and then perform them.
//
// The worker has a set of jobs that it is capable of performing. The standard
// functions for a job are Queue, Kill, and Perform. Queue will add a job to the
// queue of work of that type. Kill will empty the queue and close out any work
// that will not be completed. Perform will grab a job from the queue if one
// exists and complete that piece of work. See snapshotworkerfetchbackups.go for
// a clean example.

// TODO: A single session should be added to the worker that gets maintained
// within the work loop. All jobs performed by the worker will use the worker's
// single session.
//
// TODO: The upload and download code needs to be moved into properly separated
// subsystems.
//
// TODO: The work functions for uploading and downloading should be transitioned
// from the check();perform(); model to just the checkAndPerform(); model. See
// how the backup fetcher is implemented for an example.

import (
	"sync"
	"time"

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
	// The host pub key also serves as an id for the worker, as there is only
	// one worker per host.
	staticHostPubKey types.SiaPublicKey

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
	downloadChunks     []*unfinishedDownloadChunk // Yet unprocessed work items.
	downloadMu         sync.Mutex
	downloadTerminated bool // Has downloading been terminated for this worker?

	// Fetch backups queue for the worker.
	staticFetchBackupsJobQueue fetchBackupsJobQueue

	// Upload variables.
	unprocessedChunks         []*unfinishedUploadChunk // Yet unprocessed work items.
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
	wakeChan chan struct{} // Worker will check queues if given a wake signal.
}

// managedBlockUntilOnline will block until the worker has internet
// connectivity. 'false' will be returned if a kill signal is received or if the
// renter is shut down before internet connectivity is restored. 'true' will be
// returned if internet connectivity is successfully restored.
func (w *worker) managedBlockUntilOnline() bool {
	for !w.renter.g.Online() {
		select {
		case <-r.tg.StopChan():
			return false
		case <-w.killChan:
			return false
		case <-time.After(offlineCheckFrequency):
		}
	}
	return true
}

// staticWake needs to be called any time that a job queued.
func (w *worker) staticWake() {
	select {
	case w.wakeChan <- struct{}{}:
	default:
	}
}

// threadedWorkLoop continually checks if work has been issued to a worker. The
// work loop checks for different types of work in a specific order, forming a
// priority queue for the various types of work. It is possible for continuous
// requests for one type of work to drown out a worker's ability to perform
// other types of work.
//
// If no work is found, the worker will sleep until woken up. Because each
// iteration is stateless, it may be possible to reduce the goroutine count in
// Sia by spinning down the worker / expiring the thread when there is no work,
// and then checking if the thread exists and creating a new one if not when
// alerting / waking the worker. This will not interrupt any connections that
// the worker has because the worker object will be kept in memory via the
// worker map.
func (w *worker) threadedWorkLoop() {
	// Ensure that all queued jobs are gracefully cleaned up when the worker is
	// shut down.
	defer w.managedKillUploading()
	defer w.managedKillDownloading()
	defer w.managedKillFetchBackupsJobs()

	// Primary work loop. There are several types of jobs that the worker can
	// perform, and they are attempted with a specific priority. If any type of
	// work is attempted, the loop resets to check for higher priority work
	// again. This means that a stream of higher priority tasks can starve a
	// building set of lower priority tasks.
	//
	// 'workAttempted' indicates that there was a job to perform, and that a
	// nontrivial amount of time was spent attempting to perform the job. The
	// job may or may not have been successful, that is irrelevant.
	for {
		// Check for stop conditions on the worker.
		if !w.managedBlockUntilOnline() {
			return
		}

		// Perform any job to fetch the list of backups from the host.
		workAttempted := w.managedPerformFetchBackupsJob()
		if workAttempted {
			continue
		}

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
		case <-w.wakeChan:
			continue
		case <-w.killChan:
			return
		case <-w.renter.tg.StopChan():
			return
		}
	}
}

// newWorker will create and return a worker that is ready to receive jobs.
func (r *Renter) newWorker(hostPubKey types.SiaPublicKey) *worker {
	return &worker{
		staticHostPubKey: hostPubKey,

		killChan: make(chan struct{}),
		wakeChan: make(chan struct{}, 1),

		renter: r,
	}
}
