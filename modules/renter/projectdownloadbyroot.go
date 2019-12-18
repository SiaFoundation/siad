package renter

// projectdownloadbyroot.go creates a worker project to fetch the data of an
// underlying sector root.

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// gougingFractionDenomDownloadByRoot sets the fraction to 1/4 to ensure the
	// renter can do at least a fraction of the budgeted downloading.
	gougingFractionDenomDownloadByRoot = 4
)

// jobDownloadByRoot contains all of the information necessary to execute a
// perform download job.
type jobDownloadByRoot struct {
	// jobDownloadByRoot exists as two phases. The first is a startup phase,
	// which determines whether or not the host is capable of executing the job.
	// The second is the fetching phase, where the worker actually fetches data
	// from the remote host.
	//
	// If staticStartupCompleted is set to false, it means the job is in phase
	// one, and if startupCompleted is set to true, it means the job is in phase
	// two.
	staticProject          *projectDownloadByRoot
	staticStartupCompleted bool
}

// projectDownloadByRoot is a project to download a piece of data knowing
// nothing more than the sector root. If the root's location on the network
// cannot easily be found by looking up a cache, the project will have every
// single worker check its respective host for the root, and then will
// coordinate fetching the root among the workers that have the root.
type projectDownloadByRoot struct {
	// Information about the data that is being retrieved.
	staticRoot   crypto.Hash
	staticLength uint64
	staticOffset uint64

	// rootFound is a bool indicating that data has been discovered on the
	// network and is actively being fetched. If rootFound is set to true, any
	// new workers that discover they have the root in question will go on
	// standby.
	//
	// workersRegistered is the list of workers that are actively working on
	// either figuring out whether their host has the data, or are working on
	// actually fetching the data. This map should be empty only if there are no
	// workers at the moment that are actively tasked with work. A worker that
	// is woken from standby needs to be placed back into the list of registered
	// workers when it is removed from standby.
	//
	// workersStandby is the list of workers that have completed checking
	// whether or not the root is available on their host and have discovered
	// that the root is available, but then the worker did not start fetching
	// the data. Workers will typically end up on standby if they see that other
	// workers are actively fetching data. The standby workers get activated if
	// the other workers fetching data experience errors.
	rootFound         bool
	workersRegistered map[string]struct{}
	workersStandby    []*worker

	// Project output. Once the project has been completed, completeChan will be
	// closed. The data and error fields contain the final output for the
	// project.
	data         []byte
	err          error
	completeChan chan struct{}

	mu sync.Mutex
}

// checkGougingDownloadByRoot looks at the current renter allowance and the
// active settings for a host and determines whether a download by root job
// should be halted due to price gouging.
//
// NOTE: Currently this function treats all downloads being the stream download
// size and assumes that data is actually being appended to the host. As the
// worker gains more modification actions on the host, this check can be split
// into different checks that vary based on the operation being performed.
func checkGougingDownloadByRoot(allowance modules.Allowance, hostSettings modules.HostExternalSettings) error {
	// Check whether the base RPC price is too high.
	if !allowance.MaxRPCPrice.IsZero() && allowance.MaxRPCPrice.Cmp(hostSettings.BaseRPCPrice) < 0 {
		errStr := fmt.Sprintf("rpc price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.BaseRPCPrice, allowance.MaxRPCPrice)
		return errors.New(errStr)
	}
	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(hostSettings.DownloadBandwidthPrice) < 0 {
		errStr := fmt.Sprintf("download bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.DownloadBandwidthPrice, allowance.MaxDownloadBandwidthPrice)
		return errors.New(errStr)
	}
	// Check whether the sector access price is too high.
	if !allowance.MaxSectorAccessPrice.IsZero() && allowance.MaxSectorAccessPrice.Cmp(hostSettings.SectorAccessPrice) < 0 {
		errStr := fmt.Sprintf("sector access price of host is %v, which is above the maximum allowed by the allowance: %v", hostSettings.SectorAccessPrice, allowance.MaxSectorAccessPrice)
		return errors.New(errStr)
	}

	// If there is no allowance, general price gouging checks have to be
	// disabled, because there is no baseline for understanding what might count
	// as price gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// Check that the combined prices make sense in the context of the overall
	// allowance. The general idea is to compute the total cost of performing
	// the same action repeatedly until a fraction of the desired total resource
	// consumption established by the allowance has been reached. The fraction
	// is determined on a case-by-case basis. If the host is too expensive to
	// even satisfy a faction of the user's total desired resource consumption,
	// the action will be blocked for price gouging.
	//
	// Because of the strategy used to perform the fetch, the base rpc price and
	// the base sector access price are incurred twice.
	singleDownloadCost := hostSettings.SectorAccessPrice.Mul64(2).Add(hostSettings.BaseRPCPrice.Mul64(2)).Add(hostSettings.DownloadBandwidthPrice.Mul64(modules.StreamDownloadSize))
	fullCostPerByte := singleDownloadCost.Div64(modules.StreamDownloadSize)
	allowanceDownloadCost := fullCostPerByte.Mul64(allowance.ExpectedDownload)
	reducedCost := allowanceDownloadCost.Div64(gougingFractionDenomDownloadByRoot)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined fetch backups pricing of host yields %v, which is more than the renter is willing to pay for storage: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}

// callPerformJobDownloadByRoot will perform a download by root job.
func (jdbr *jobDownloadByRoot) callPerformJobDownloadByRoot(w *worker) {
	if jdbr.staticStartupCompleted {
		jdbr.staticProject.managedResumeJobDownloadByRoot(w)
	} else {
		jdbr.staticProject.managedStartJobDownloadByRoot(w)
	}
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
	if len(pdbr.workersRegistered) == 0 {
		// Sanity check - a worker should only go on standby if there are
		// registered workers actively trying to download the data. If those
		// workers fail and remove themselves, they should wake a standby worker
		// before removing themselves from the project, meaning that there
		// should never be a case where the list of registered workers is empty
		// but the list of standby workers is not empty.
		if len(pdbr.workersStandby) != 0 {
			build.Critical("pdbr has standby workers but no registered workers:", len(pdbr.workersStandby))
		}
		pdbr.err = errors.New("workers were unable to recover the data by sector root - all workers failed")
		close(pdbr.completeChan)
	}
}

// managedResult is a convenience function to return the result of the project.
func (pdbr *projectDownloadByRoot) managedResult() ([]byte, error) {
	// Sanity check - should not be accessing the error of the pdbr until the
	// completeChan has closed.
	select {
	case <-pdbr.completeChan:
	default:
		build.Critical("error field of pdbr is being accessed before the pdbr has finished")
	}

	pdbr.mu.Lock()
	defer pdbr.mu.Unlock()
	return pdbr.data, pdbr.err
}

// managedResumeJobDownloadByRoot is called after a worker has confirmed that a
// root exists on a host, and after the worker has gained the imperative to
// fetch the data from the host.
func (pdbr *projectDownloadByRoot) managedResumeJobDownloadByRoot(w *worker) {
	// Fetch a session to use in retrieving the sector.
	downloader, err := w.renter.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		// TODO: run error code here to indicate to the worker that attempts to
		// grab the downloader are failing.
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because downloader could not be opened:", err)
		pdbr.managedWakeStandbyWorker()
		pdbr.managedRemoveWorker(w)
		return
	}
	defer downloader.Close()

	// Check for price gouging before completing the job.
	allowance := w.renter.hostContractor.Allowance()
	hostSettings := downloader.HostSettings()
	err = checkGougingDownloadByRoot(allowance, hostSettings)
	if err != nil {
		//  TODO: run error code here to indicate to the worker that price
		//  gouging warnings are being hit.
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because gouging protection kicked in:", err)
		pdbr.managedWakeStandbyWorker()
		pdbr.managedRemoveWorker(w)
		return
	}

	// Fetch the data. Need to ensure that the length is a factor of 64, need to
	// add and remove padding.
	padding := 64 - pdbr.staticLength%64
	if padding == 64 {
		padding = 0
	}
	sectorData, err := downloader.Download(pdbr.staticRoot, uint32(pdbr.staticOffset), uint32(pdbr.staticLength+padding))
	// If the fetch was unsuccessful, a standby worker needs to be activated.
	if err != nil {
		// TODO: run error code here to indicate that worker download attempts
		// are failing. It's already been established that the host has the
		// sector.
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because the full root download failed:", err)
		pdbr.managedWakeStandbyWorker()
		pdbr.managedRemoveWorker(w)
		return
	}

	// Fetch was successful, update the data in the pdbr and perform a
	// successful close. Make sure to strip the padding when returning the data.
	pdbr.mu.Lock()
	pdbr.data = sectorData[:pdbr.staticLength]
	pdbr.mu.Unlock()
	close(pdbr.completeChan)
}

// managedStartJobDownloadByRoot will execute the first stage of downloading
// data by merkle root for a worker. The first stage consists of determining
// whether or not the worker's host has the merkle root in question.
func (pdbr *projectDownloadByRoot) managedStartJobDownloadByRoot(w *worker) {
	// Determine whether the host has the root. This is accomplished by
	// performing a download for only one byte.
	//
	// Fetch a session to use in retrieving the sector.
	downloader, err := w.renter.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		// TODO: run error code here to indicate to the worker that attempts to
		// grab the downloader are failing.
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because downloader could not be opened:", err)
		pdbr.managedRemoveWorker(w)
		return
	}
	defer downloader.Close()
	// Check for price gouging before completing the job.
	allowance := w.renter.hostContractor.Allowance()
	hostSettings := downloader.HostSettings()
	err = checkGougingDownloadByRoot(allowance, hostSettings)
	if err != nil {
		//  TODO: run error code here to indicate to the worker that price
		//  gouging warnings are being hit.
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because gouging protection kicked in:", err)
		pdbr.managedRemoveWorker(w)
		return
	}
	// Try to fetch one byte.
	_, err = downloader.Download(pdbr.staticRoot, 0, 64)
	if err != nil {
		w.renter.log.Debugln("worker failed a projectDownloadByRoot because the initial root download failed:", err)
		pdbr.managedRemoveWorker(w)
		return
	}

	// The host has the root. Check in with the project and see if the root
	// needs to be fetched. If 'rootFound' is set to false, it means that
	// nobody is actively fetching the root.
	pdbr.mu.Lock()
	if pdbr.rootFound {
		pdbr.workersStandby = append(pdbr.workersStandby, w)
		pdbr.mu.Unlock()
		return
	}
	pdbr.rootFound = true
	pdbr.mu.Unlock()

	// Have the worker attempt the full download.
	pdbr.managedResumeJobDownloadByRoot(w)
	return
}

// managedWakeStandbyWorker is called when a worker that was performing actual
// download work has failed and needs to be replaced. If there are any standby
// workers, one of the standby workers will assume its place.
//
// managedWakeStandbyWorker assumes that rootFound is currently set to true,
// because it will be called by a worker that failed an had set rootFound to
// true.
func (pdbr *projectDownloadByRoot) managedWakeStandbyWorker() {
	// If there are no workers on standby, set rootFound to false, indicating
	// that no workers are actively fetching the piece; any worker that finds
	// the piece should immediately start fetching it.
	pdbr.mu.Lock()
	if len(pdbr.workersStandby) == 0 {
		pdbr.rootFound = false
		pdbr.mu.Unlock()
		return
	}
	// There is a standby worker that has found the piece previously, pop it off
	// of the array.
	newWorker := pdbr.workersStandby[0]
	pdbr.workersStandby = pdbr.workersStandby[1:]
	pdbr.mu.Unlock()

	// Schedule a job with the worker to resume the download. Can't download
	// directly because any work that is being performed needs to go through the
	// worker so that the worker can actively control how the connection is
	// used.
	jdbr := jobDownloadByRoot{
		staticProject:          pdbr,
		staticStartupCompleted: true,
	}
	newWorker.callQueueJobDownloadByRoot(jdbr)
}

// DownloadByRoot will spin up a project to locate a root and then download that
// root.
//
// TODO: Update the function, or perhaps create a separate project, to handle
// intra-root erasure coding. For speed, going to need some updates anyway
// that's better about worker selection and making sure we go for hosts that can
// provide excellent ttfb, while making the selection process aware of the
// workload of existing workers.
func (r *Renter) DownloadByRoot(root crypto.Hash, offset, length uint64) ([]byte, error) {
	// Create the download by root project.
	pdbr := &projectDownloadByRoot{
		staticRoot:   root,
		staticLength: length,
		staticOffset: offset,

		workersRegistered: make(map[string]struct{}),

		completeChan: make(chan struct{}),
	}

	// Give the project to every worker. The list of workers needs to be fetched
	// first, and then the job can be queued because cleanup of the project
	// assumes that no more workers will be added to the project once the first
	// worker has begun work.
	wp := r.staticWorkerPool
	wp.mu.RLock()
	workers := make([]*worker, 0, len(wp.workers))
	for _, w := range wp.workers {
		pdbr.workersRegistered[w.staticHostPubKeyStr] = struct{}{}
		workers = append(workers, w)
	}
	wp.mu.RUnlock()
	// Queue the jobs in the workers.
	jdbr := jobDownloadByRoot{
		staticProject:          pdbr,
		staticStartupCompleted: false,
	}
	for _, w := range workers {
		w.callQueueJobDownloadByRoot(jdbr)
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
