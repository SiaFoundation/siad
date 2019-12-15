package renter

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// downloadByRootGougingFractionDenom sets the fraction to 1/4 to ensure the
	// renter can do at least a fraction of the budgeted downloading.
	downloadByRootGougingFractionDenom = 4
)

// jobQueueDownloadByRoot is a queue of jobs that the worker need to perform to
// download data by root.
type jobQueueDownloadByRoot struct {
	queue []*projectDownloadByRoot
	mu sync.Mutex
}

// checkGougingDownloadByRoot looks at the current renter allowance and the
// active settings for a host and determines whether an backup fetch should be
// halted due to price gouging.
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
	reducedCost := allowanceDownloadCost.Div64(downloadByRootGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined fetch backups pricing of host yields %v, which is more than the renter is willing to pay for storage: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}

// callQueueJobDownloadByRoot adds a downloadByRoot job to the worker's list of
// stuff to do.
//
// TODO: I'm wondering if there's a race condition in the other callQueue
// functions on the worker that can be triggered such that the queue has an item
// appended to it after the kill function has executed.
func (w *worker) callQueueJobDownloadByRoot(pdbr *projectDownloadByRoot) error {
	// Pull the queue out of the worker. To prevent a race condition where the
	// worker can have items appended to its queue after it has been killed, a
	// check that the worker hasn't been killed needs to be performed after
	// acquiring the lock.
	w.staticJobQueueDownloadByRoot.mu.Lock()
	if w.staticKilled() {
		w.staticJobQueueDownloadByRoot.mu.Unlock()
		return errors.New("unable to queue job: worker has been killed")
	}
	w.staticJobQueueDownloadByRoot.queue = append(w.staticJobQueueDownloadByRoot.queue, pdbr)
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
	queue := w.staticJobQueueDownloadByRoot.queue
	w.staticJobQueueDownloadByRoot.queue = nil
	w.staticJobQueueDownloadByRoot.mu.Unlock()

	// Loop through the queue and remove the worker from each job.
	for _, pdbr := range queue {
		// TODO: Log that the worker is failing the download because of
		// receiving the kill signal.
		pdbr.managedRemoveWorker(w)
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
	pdbr := w.staticJobQueueDownloadByRoot.queue[0]
	w.staticJobQueueDownloadByRoot.queue = w.staticJobQueueDownloadByRoot.queue[1:]
	w.staticJobQueueDownloadByRoot.mu.Unlock()

	// Determine whether the host has the root. This is accomplished by
	// performing a download for only one byte.
	//
	// Fetch a session to use in retrieving the sector.
	downloader, err := w.renter.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		pdbr.managedRemoveWorker(w)
		return true
	}
	defer downloader.Close()
	// Check for price gouging before completing the job.
	allowance := w.renter.hostContractor.Allowance()
	hostSettings := downloader.HostSettings()
	err = checkGougingDownloadByRoot(allowance, hostSettings)
	if err != nil {
		pdbr.managedRemoveWorker(w)
		return true
	}
	// Try to fetch one byte.
	_, err = downloader.Download(pdbr.staticRoot, 0, 1)
	if err != nil {
		pdbr.managedRemoveWorker(w)
		return true
	}

	// The host has the root. Check in with the project and see if the root
	// needs to be fetched. If 'pieceFound' is set to false, it means that
	// nobody is actively fetching the root.
	pdbr.mu.Lock()
	if pdbr.pieceFound {
		pdbr.workersStandby = append(pdbr.workersStandby, w)
		pdbr.mu.Unlock()
		return true
	}
	pdbr.pieceFound = true
	pdbr.mu.Unlock()

	// Have the worker attempt the full download.
	w.managedResumeJobPerformDownloadByRoot(pdbr)
	return true
}

// managedResumeJobPerformDownloadByRoot is called after a worker has confirmed
// that a root exists on a host, and after the worker has gained the imperative
// to fetch the data from the host.
func (w *worker) managedResumeJobPerformDownloadByRoot(pdbr *projectDownloadByRoot) {
	// Fetch a session to use in retrieving the sector.
	downloader, err := w.renter.hostContractor.Downloader(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
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
		pdbr.managedWakeStandbyWorker()
		pdbr.managedRemoveWorker(w)
		return
	}

	// Fetch the data.
	sectorData, err := downloader.Download(pdbr.staticRoot, uint32(pdbr.staticOffset), uint32(pdbr.staticLength))
	// If the fetch was unsuccessful, a standby worker needs to be activated
	if err != nil {
		pdbr.managedWakeStandbyWorker()
		pdbr.managedRemoveWorker(w)
		return
	}

	// Fetch was successful, update the data in the pdbr and perform a
	// successful close.
	pdbr.mu.Lock()
	pdbr.data = sectorData
	pdbr.mu.Unlock()
	close(pdbr.completeChan)
}
