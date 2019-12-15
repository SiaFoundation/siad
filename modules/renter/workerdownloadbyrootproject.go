package renter

// projectdownloadbyroot.go creates a worker project to fetch the data of an
// underlying sector root.

// TODO: Eventually this download by root system will feture a cache, whereby
// the viewnode queries all hosts and records every sector root that each of
// them is storing. This allows the viewnode to skip the expensive stage of
// figuring out where a root is by querying every single host one at a time -
// it'll already know.
//
// Problem with this plan: the viewnode won't have any super recent roots, those
// will take a bit longer to fetch.

// TODO: Mantra: first make it work, then make it worker faster, repeat as
// necessary.

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/errors"
)

// projectDownloadByRoot is a project to download a piece of data knowing
// nothing more than the sector root. The plan of action is to query every
// single host to see whether they have the sector. This all happens
// concurrently.
//
// In the first implementation, the first worker to get back a response will
// attempt to download the whole sector. In later implementations, the speed of
// this download will be measured and if the speed is unsatisfactory, other
// workers who found the sector will spin up to download the data alongside the
// first worker.
type projectDownloadByRoot struct {
	// Information about the data that is being retrieved.
	staticRoot   crypto.Hash
	staticLength uint64
	staticOffset uint64

	// workersRegistered is the list of workers that haven't yet checked whether
	// or not the download root exists on their host. If a worker performs a
	// check and finds that the data is not available, the worker will be
	// removed from workersRegistered. If the worker finds the sector and then
	// chooses not to download the sector because other workers are already
	// performing the task, the worker will add itself to the list of standby
	// workers so that it can be used if one of the other workers fails. When
	// the worker starts the job again after coming out of standby, it adds
	// itself back into workersRegistered.
	//
	// If a worker starts a job and fails, it needs to remove itself from
	// workersRegistered, and it also needs to wake all of the standby workers
	// so that those workers can try to perform the job again. If there are no
	// workers on standby, and also no workers registered, then the project has
	// failed and an error needs to be set, and the completeChan needs to be
	// cloesd.
	workersRegistered map[string]struct{}
	workersStandby []*worker

	// pieceFound is used to coordinate the workers and ensure that only one
	// fetches the root.
	//
	// This will eventually be extended to have a more sophisticated
	// coordination mechanism that allows multiple workers to fetch the root in
	// parallel based on the roots intra-root erasure coding.
	pieceFound bool

	// Termination conditions.
	data         []byte
	err          error
	completeChan chan struct{}

	mu sync.Mutex
}

// managedErr is a convenience function to return the error of the pdbr.
func (pdbr *projectDownloadByRoot) managedErr() error {
	// Sanity check - should not be accessing the error of the pdbr until the
	// completeChan has closed.
	select {
	case <-pdbr.completeChan:
	default:
		build.Critical("error field of pdbr is being accessed before the pdbr has finished")
	}

	pdbr.mu.Lock()
	defer pdbr.mu.Unlock()
	return pdbr.err
}

// managedRemoveWorker will remove a worker from the project.
func (pdbr *projectDownloadByRoot) managedRemoveWorker(w *worker) {
	pdbr.mu.Lock()
	defer pdbr.mu.Unlock()

	// Delete the worker from the list of registered workers.
	delete(pdbr.workersRegistered, w.staticHostPubKeyStr)

	// Delete every instance of the worker in the list of standby workers. The
	// worker should only be in the list once, but we check teh whole list
	// anyway.
	for i := 0; i < len(pdbr.workersStandby); i++ {
		if pdbr.workersStandby[i] == w {
			pdbr.workersStandby = append(pdbr.workersStandby[:i], pdbr.workersStandby[i+1:]...)
			i-- // Since the array has been shifted, adjust the iterator to ensure every item is visited.
		}
	}

	// Check whether the pdbr is already completed. If so, nothing else needs to
	// be done.
	select {
	case <-pdbr.completeChan:
		return
	default:
	}

	// Sector download is not yet complete. Check whether this is the last
	// worker in the pdbr, requiring a shutdown / failure to be sent.
	if len(pdbr.workersRegistered) == 0 && len(pdbr.workersStandby) == 0 {
		pdbr.err = errors.New("workers were unable to recover the data by sector root - all workers failed")
		close(pdbr.completeChan)
	}
}

// managedWakeStandbyWorker is called when a worker that was performing the
// actual download has failed and needs to be replaced. If there are any standby
// workers, one of the standby workers will assume its place.
//
// managedWakeStandbyWorker assumes that pieceFound is currently set to true,
// because it will be called by a worker that failed an had set pieceFound to
// true.
func (pdbr *projectDownloadByRoot) managedWakeStandbyWorker() {
	// If there are no workers on standby, set pieceFound to false, indicating
	// that no workers are actively fetching the piece.
	pdbr.mu.Lock()
	if len(pdbr.workersStandby) == 0 {
		pdbr.pieceFound = false
		pdbr.mu.Unlock()
		return
	}
	// There is a standby worker, pop it off of the array.
	newWorker := pdbr.workersStandby[0]
	pdbr.workersStandby = pdbr.workersStandby[1:]
	pdbr.mu.Unlock()

	// Perform the download after releasing the pdbr lock.
	newWorker.managedResumeJobPerformDownloadByRoot(pdbr)
}

// downloadByRoot will spin up a project to locate a root and then download that
// root.
func (r *Renter) downloadByRoot(root crypto.Hash, offset, length uint64) ([]byte, error) {
	// Create the download by root project.
	pdbr := &projectDownloadByRoot{
		staticRoot:   root,
		staticLength: length,
		staticOffset: offset,

		workersRegistered: make(map[string]struct{}),

		completeChan: make(chan struct{}),
	}

	// Give the project to every worker. Two stage loop to avoid a double lock.
	r.staticWorkerPool.mu.RLock()
	workers := make([]*worker, len(r.staticWorkerPool.workers))
	for _, w := range r.staticWorkerPool.workers {
		workers = append(workers, w)
	}
	r.staticWorkerPool.mu.RUnlock()
	// Register all of the workers in the pdbr.
	for _, w := range workers {
		pdbr.workersRegistered[w.staticHostPubKeyStr] = struct{}{}
	}
	// Queue the jobs in the workers. The queueing has to be performed after all
	// of the workers have been registered because cleanup assumes that every
	// worker has been properly registered before any workers have started to
	// unregister themselves. Workers can start to unregister as soon as the job
	// has been queued.
	for _, w := range workers {
		w.callQueueJobDownloadByRoot(pdbr)
	}

	// Block until the project has completed.
	<-pdbr.completeChan
	pdbr.mu.Lock()
	err := pdbr.err
	data := pdbr.data
	pdbr.mu.Unlock()
	if err != nil {
		return nil, errors.AddContext(err, "unable to fetch sector root from the network")
	}
	return data, nil
}
