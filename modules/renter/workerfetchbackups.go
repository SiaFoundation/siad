package renter

// workerfetchbackups.go contains all of the code related to using the worker to
// fetch the list of snapshot backups available on a particular host.

// TODO: Currently the backups are fetched using a separate session, when the
// worker code is switched over to having a common session we should start using
// that common session. Implementation in managedPerformFetchBackupsJob.
//
// TODO: The conversion from the []snapshotEntry to the []modules.UploadedBackup
// is a conversion that should probably happen in the snapshot subsystem, or at
// least use a helper method from the snapshot subsystem.

import (
	"fmt"
	"sync"

	"gitlab.com/NebulousLabs/errors"

	"gitlab.com/NebulousLabs/Sia/modules"
)

const (
	// fetchBackupsGougingFractionDenom sets the fraction to 1/100 because
	// fetching backups is important, so there is less sensitivity to gouging.
	// Also, this is a rare operation.
	fetchBackupsGougingFractionDenom = 100
)

// fetchBackupsJobQueue is the primary structure for managing fetch backup jobs
// from the worker.
type fetchBackupsJobQueue struct {
	queue []chan fetchBackupsJobResult
	mu    sync.Mutex
}

// fetchBackupsJobResult contains the result from fetching a bunch of backups
// from the host.
type fetchBackupsJobResult struct {
	err             error
	uploadedBackups []modules.UploadedBackup
}

// staticCheckFetchBackupsGouging looks at the current renter allowance and the
// active settings for a host and determines whether an backup fetch should be
// halted due to price gouging.
//
// NOTE: Currently this function treats all downloads being the stream download
// size and assumes that data is actually being appended to the host. As the
// worker gains more modification actions on the host, this check can be split
// into different checks that vary based on the operation being performed.
func staticCheckFetchBackupsGouging(allowance modules.Allowance, hostSettings modules.HostExternalSettings) error {
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
	// the host is block for price gouging.
	singleDownloadCost := hostSettings.SectorAccessPrice.Add(hostSettings.BaseRPCPrice).Add(hostSettings.DownloadBandwidthPrice.Mul64(modules.StreamDownloadSize))
	fullCostPerByte := singleDownloadCost.Div64(modules.StreamDownloadSize)
	allowanceDownloadCost := fullCostPerByte.Mul64(allowance.ExpectedDownload)
	reducedCost := allowanceDownloadCost.Div64(fetchBackupsGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined fetch backups pricing of host yields %v, which is more than the renter is willing to pay for storage: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}

// callQueueFetchBackupsJob will add the fetch backups job to the worker's
// queue. A channel will be returned, this channel will have the result of the
// job returned down it when the job is completed.
//
// Testing happens via an integration test. siatest/renter/TestRemoteBackup has
// a test where a backup is fetched from a host, an action which reaches this
// code.
func (w *worker) callQueueFetchBackupsJob() chan fetchBackupsJobResult {
	resultChan := make(chan fetchBackupsJobResult)
	w.staticFetchBackupsJobQueue.mu.Lock()
	w.staticFetchBackupsJobQueue.queue = append(w.staticFetchBackupsJobQueue.queue, resultChan)
	w.staticFetchBackupsJobQueue.mu.Unlock()
	w.staticWake()
	return resultChan
}

// managedKillFetchBackupsJobs will throw an error for all queued backup jobs,
// as they will not complete due to the worker being shut down.
func (w *worker) managedKillFetchBackupsJobs() {
	w.staticFetchBackupsJobQueue.mu.Lock()
	for _, job := range w.staticFetchBackupsJobQueue.queue {
		result := fetchBackupsJobResult{
			err: errors.New("worker was killed before backups could be retrieved"),
		}
		job <- result
	}
	w.staticFetchBackupsJobQueue.mu.Unlock()
}

// managedPerformFetchBackupsJob will fetch the list of backups from the host
// and return them down the provided struct.
//
// Testing happens via an integration test. siatest/renter/TestRemoteBackup has
// a test where a backup is fetched from a host, an action which reaches this
// code.
func (w *worker) managedPerformFetchBackupsJob() bool {
	// Check whether there is any work to be performed.
	var resultChan chan fetchBackupsJobResult
	w.staticFetchBackupsJobQueue.mu.Lock()
	if len(w.staticFetchBackupsJobQueue.queue) == 0 {
		w.staticFetchBackupsJobQueue.mu.Unlock()
		return false
	}
	resultChan = w.staticFetchBackupsJobQueue.queue[0]
	w.staticFetchBackupsJobQueue.queue = w.staticFetchBackupsJobQueue.queue[1:]
	w.staticFetchBackupsJobQueue.mu.Unlock()

	// Fetch a session to use in retrieving the backups.
	session, err := w.renter.hostContractor.Session(w.staticHostPubKey, w.renter.tg.StopChan())
	if err != nil {
		result := fetchBackupsJobResult{
			err: errors.AddContext(err, "unable to acquire session"),
		}
		resultChan <- result
		return true
	}
	defer session.Close()

	// Check for price gouging before completing the job.
	allowance := w.renter.hostContractor.Allowance()
	hostSettings := session.HostSettings()
	err = staticCheckFetchBackupsGouging(allowance, hostSettings)
	if err != nil {
		result := fetchBackupsJobResult{
			err: errors.AddContext(err, "price gouging check failed for fetch backups job"),
		}
		resultChan <- result
		return true
	}

	backups, err := w.renter.callFetchHostBackups(session)
	result := fetchBackupsJobResult{
		uploadedBackups: backups,
		err:             errors.AddContext(err, "unable to download snapshot table"),
	}
	resultChan <- result
	return true
}
