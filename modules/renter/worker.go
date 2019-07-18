package renter

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
//
// TODO: We should add a common session to the worker that persists to eliminate
// the inherent latency and round trip overheads associated with opening a
// session. This will also make Sia faster.
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
	downloadChan       chan struct{}              // Notifications of new work. Takes priority over uploads.
	downloadChunks     []*unfinishedDownloadChunk // Yet unprocessed work items.
	downloadMu         sync.Mutex
	downloadTerminated bool // Has downloading been terminated for this worker?

	// Fetch backups queue for the worker.
	staticFetchBackupsJobQueue fetchBackupsJobQueue

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
	wakeChan chan struct{} // Worker will check queues if given a wake signal.
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
	defer w.managedKillUploading()
	defer w.managedKillDownloading()
	defer w.managedKillFetchBackupsJobs()

	for {
		// Check if there is a query from the user to fetch a backup from this
		// host.
		fetchBackupsJob := w.managedNextFetchBackupsJob()
		if fetchBackupsJob != nil {
			w.managedPerformFetchBackupsJob(fetchBackupsJob)
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
		//
		// TODO: Condense these to a single channel.
		select {
		case <-w.wakeChan:
			continue
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
