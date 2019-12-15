package renter

import (
	"sync"
)

// jobDownloadByRoot contains all of the information necessary to execute a
// perform download job.
type jobDownloadByRoot struct {
	// jobDownloadByRoot exists as two phases. The first is a startup phase,
	// which determines whether or not the host is capable of executing the job.
	// The second is the fetching phase, where the worker actually fetches data
	// from the remote host.
	//
	// If startupCompleted is set to false, it means the job is in phase one,
	// and if startupCompleted is set to true, it means the job is in phase two.
	project          *projectDownloadByRoot
	startupCompleted bool
}

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
// download by root prjects.
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
		w.renter.log.Debugln("worker was killed before being able to attempt a downloadByRoot job")
		jdbr.project.managedRemoveWorker(w)
	}
}

// managedPerformJobDownloadByRoot will attempt to download the root requested
// by the project from the host.
func (w *worker) managedPerformJobDownloadByRoot() bool {
	// Fetch work if there is any work to be done.
	w.staticJobQueueDownloadByRoot.mu.Lock()
	if len(w.staticJobQueueDownloadByRoot.queue) == 0 {
		w.staticJobQueueDownloadByRoot.mu.Unlock()
		return false
	}
	jdbr := w.staticJobQueueDownloadByRoot.queue[0]
	w.staticJobQueueDownloadByRoot.queue = w.staticJobQueueDownloadByRoot.queue[1:]
	w.staticJobQueueDownloadByRoot.mu.Unlock()
	jdbr.project.managedStartJobPerformDownloadByRoot(w)
	return true
}
