package renter

import (
	"bytes"
	"context"
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

const (
	// snapshotDownloadGougingFractionDenom sets the fraction to 1/100 because
	// downloading snapshots is important, so there is less sensitivity to
	// gouging. Also, this is a rare operation.
	snapshotDownloadGougingFractionDenom = 100
)

type (
	// jobDownloadSnapshot is a job for the worker to download a snapshot from
	// its respective host.
	jobDownloadSnapshot struct {
		staticResponseChan chan *jobDownloadSnapshotResponse

		*jobGeneric
	}

	// jobDownloadSnapshotQueue contains the download jobs.
	jobDownloadSnapshotQueue struct {
		*jobGenericQueue
	}

	// jobDownloadSnapshotResponse contains the response to an upload snapshot
	// job.
	jobDownloadSnapshotResponse struct {
		staticSnapshots []snapshotEntry
		staticErr       error
	}
)

// checkDownloadSnapshotGouging looks at the current renter allowance and the
// active settings for a host and determines whether a snapshot upload should be
// halted due to price gouging.
func checkDownloadSnapshotGouging(allowance modules.Allowance, pt modules.RPCPriceTable) error {
	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(pt.DownloadBandwidthCost) < 0 {
		errStr := fmt.Sprintf("download bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v", pt.DownloadBandwidthCost, allowance.MaxDownloadBandwidthPrice)
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
	expectedDL := modules.SectorSize
	rpcCost := modules.MDMInitCost(&pt, 48, 1).Add(modules.MDMReadCost(&pt, expectedDL)) // 48 bytes is the length of a single instruction read program
	bandwidthCost := pt.DownloadBandwidthCost.Mul64(expectedDL)
	fullCostPerByte := rpcCost.Add(bandwidthCost).Div64(expectedDL)
	allowanceDownloadCost := fullCostPerByte.Mul64(allowance.ExpectedDownload)
	reducedCost := allowanceDownloadCost.Div64(snapshotDownloadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined download snapshot pricing of host yields %v, which is more than the renter is willing to pay for storage: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}

// callDiscard will discard this job, sending an error down the response
// channel.
func (j *jobDownloadSnapshot) callDiscard(err error) {
	resp := &jobDownloadSnapshotResponse{
		staticErr: errors.Extend(err, ErrJobDiscarded),
	}
	w := j.staticQueue.staticWorker()
	w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- resp:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
}

// callExecute will perform an upload snapshot job for the worker.
func (j *jobDownloadSnapshot) callExecute() {
	w := j.staticQueue.staticWorker()

	// Defer a function to send the result down a channel.
	var snapshots []snapshotEntry
	var err error
	defer func() {
		resp := &jobDownloadSnapshotResponse{
			staticErr:       err,
			staticSnapshots: snapshots,
		}
		w.renter.tg.Launch(func() {
			select {
			case j.staticResponseChan <- resp:
			case <-j.staticCtx.Done():
			case <-w.renter.tg.StopChan():
			}
		})

		// Report a failure to the queue if this job had an error.
		if resp.staticErr != nil {
			j.staticQueue.callReportFailure(resp.staticErr)
		} else {
			j.staticQueue.callReportSuccess()
		}
	}()

	// Check for gouging
	allowance := w.staticCache().staticRenterAllowance
	pt := w.staticPriceTable().staticPriceTable
	err = checkDownloadSnapshotGouging(allowance, pt)
	if err != nil {
		err = errors.AddContext(err, "price gouging check failed for download snapshot job")
		return
	}

	// Perform the actual download
	snapshots, err = w.renter.managedDownloadSnapshotTable(w)
	if err != nil && errors.Contains(err, errEmptyContract) {
		err = nil
	}

	return
}

// callExpectedBandwidth returns the amount of bandwidth this job is expected to
// consume.
func (j *jobDownloadSnapshot) callExpectedBandwidth() (ul, dl uint64) {
	// Estimate 50kb in overhead for upload and download, and then 4 MiB
	// necessary to send the actual full sector payload.
	return 50e3 + 1<<22, 50e3
}

// initJobUploadSnapshotQueue will initialize the upload snapshot job queue for
// the worker.
func (w *worker) initJobDownloadSnapshotQueue() {
	if w.staticJobDownloadSnapshotQueue != nil {
		w.renter.log.Critical("should not be double initializng the upload snapshot queue")
		return
	}

	w.staticJobDownloadSnapshotQueue = &jobDownloadSnapshotQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// DownloadSnapshotTable is a convenience method to runs a DownloadSnapshot job
// on a worker and returns the snapshot table.
func (w *worker) DownloadSnapshotTable(ctx context.Context) ([]snapshotEntry, error) {
	downloadSnapshotRespChan := make(chan *jobDownloadSnapshotResponse)
	jus := &jobDownloadSnapshot{
		staticResponseChan: downloadSnapshotRespChan,

		jobGeneric: newJobGeneric(ctx, w.staticJobDownloadSnapshotQueue, nil),
	}

	// Add the job to the queue.
	if !w.staticJobDownloadSnapshotQueue.callAdd(jus) {
		return nil, errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobDownloadSnapshotResponse
	select {
	case <-ctx.Done():
		return nil, errors.New("DownloadSnapshotTable interrupted")
	case resp = <-downloadSnapshotRespChan:
	}

	if resp.staticErr != nil {
		return nil, errors.AddContext(resp.staticErr, "DownloadSnapshotTable failed")
	}
	return resp.staticSnapshots, nil
}

// FetchBackups is a convenience method to runs a DownloadSnapshot job on a
// worker and formats the response to a list of UploadedBackup objects.
func (w *worker) FetchBackups(ctx context.Context) ([]modules.UploadedBackup, error) {
	snapshots, err := w.DownloadSnapshotTable(ctx)
	if err != nil {
		return nil, errors.AddContext(err, "FetchBackups failed to download snapshot table")
	}

	// Format the response and return the response to the requester.
	uploadedBackups := make([]modules.UploadedBackup, len(snapshots))
	for i, e := range snapshots {
		uploadedBackups[i] = modules.UploadedBackup{
			Name:           string(bytes.TrimRight(e.Name[:], types.RuneToString(0))),
			UID:            e.UID,
			CreationDate:   e.CreationDate,
			Size:           e.Size,
			UploadProgress: 100,
		}
	}
	return uploadedBackups, nil
}
