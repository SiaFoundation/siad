package renter

import (
	"sync"
)

// jobQueueDownloadByRoot is a queue of jobs that the worker need to perform to
// download data by root.
type jobQueueDownloadByRoot struct {
	queue []jobDownloadByRoot
	mu    sync.Mutex
}

// managedQueueJobDownloadByRoot adds a downloadByRoot job to the worker's queue.
func (w *worker) callQueueJobDownloadByRoot(jdbr jobDownloadByRoot) error {
	w.staticJobQueueDownloadByRoot.mu.Lock()
	w.staticJobQueueDownloadByRoot.queue = append(w.staticJobQueueDownloadByRoot.queue, jdbr)
	w.staticJobQueueDownloadByRoot.mu.Unlock()
	w.staticWake()
	return nil
}

// managedKillJobsDownloadByRoot will remove the worker from all pending
// download by root projects.
func (w *worker) managedKillJobsDownloadByRoot() {
	// Grab the queue from the worker, and then nil out the queue so that no
	// other thread can get access to the objects within. managedRemoveWorker
	// requires a lock, which is why the queue has to be extracted instead of
	// being operated on while the queue is locked.
	w.staticJobQueueDownloadByRoot.mu.Lock()
	queueJobDownloadByRoot := w.staticJobQueueDownloadByRoot.queue
	w.staticJobQueueDownloadByRoot.queue = nil
	w.staticJobQueueDownloadByRoot.mu.Unlock()

	// Loop through the queue and remove the worker from each job.
	for _, jdbr := range queueJobDownloadByRoot {
		w.renter.log.Debugf("worker %v was killed before being able to attempt a downloadByRoot job", w.staticHostPubKeyStr)
		jdbr.staticProject.managedRemoveWorker(w)
	}
}

// managedLaunchJobDownloadByRoot will attempt to download the root requested by
// the project from the host.
func (w *worker) managedLaunchJobDownloadByRoot() bool {
	// Fetch work if there is any work to be done.
	w.staticJobQueueDownloadByRoot.mu.Lock()
	if len(w.staticJobQueueDownloadByRoot.queue) == 0 {
		w.staticJobQueueDownloadByRoot.mu.Unlock()
		return false
	}
	jdbr := w.staticJobQueueDownloadByRoot.queue[0]
	w.staticJobQueueDownloadByRoot.queue = w.staticJobQueueDownloadByRoot.queue[1:]
	w.staticJobQueueDownloadByRoot.mu.Unlock()
	jdbr.callPerformJobDownloadByRoot(w)
	return true
}
