package renter

import (
	"context"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"

	"gitlab.com/NebulousLabs/errors"
)

type (
	// jobRenew contains information about a Renew query.
	jobRenew struct {
		staticResponseChan       chan *jobRenewResponse
		staticTransactionBuilder modules.TransactionBuilder
		staticParams             modules.ContractParams
		staticFCID               types.FileContractID

		*jobGeneric
	}

	// jobRenewQueue is a list of Renew queries that have been assigned to the
	// worker.
	jobRenewQueue struct {
		*jobGenericQueue
	}

	// jobRenewResponse contains the result of a Renew query.
	jobRenewResponse struct {
		staticNewContract modules.RenterContract
		staticTxnSet      []types.Transaction
		staticErr         error

		// The worker is included in the response so that the caller can listen
		// on one channel for a bunch of workers and still know which worker
		// successfully found the sector root.
		staticWorker *worker
	}
)

// renewJobExpectedBandwidth is a helper function that returns the expected
// bandwidth consumption of a renew job.
func renewJobExpectedBandwidth() (ul, dl uint64) {
	ul = 8760
	dl = 4380
	return
}

// callDiscard will discard a job, sending the provided error.
func (j *jobRenew) callDiscard(err error) {
	w := j.staticQueue.staticWorker()
	w.renter.tg.Launch(func() {
		response := &jobRenewResponse{
			staticErr: errors.Extend(err, ErrJobDiscarded),
		}
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})
}

// callExecute will run the renew job.
func (j *jobRenew) callExecute() {
	w := j.staticQueue.staticWorker()

	// Proactively try to fix a revision mismatch.
	w.externTryFixRevisionMismatch()

	newContract, txnSet, err := w.managedRenew(j.staticFCID, j.staticParams, j.staticTransactionBuilder)

	// If the error could be caused by a revision number mismatch,
	// signal it by setting the flag.
	if errCausedByRevisionMismatch(err) {
		w.staticSetSuspectRevisionMismatch()
		w.staticWake()
	}

	// Send the response.
	response := &jobRenewResponse{
		staticErr:         err,
		staticNewContract: newContract,
		staticTxnSet:      txnSet,

		staticWorker: w,
	}
	w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCtx.Done():
		case <-w.renter.tg.StopChan():
		}
	})

	// Report success or failure to the queue.
	if err != nil {
		j.staticQueue.callReportFailure(err)
		return
	}
	// Update worker cache with the new fcid.
	w.managedUpdateCache()
	j.staticQueue.callReportSuccess()
}

// callExpectedBandwidth returns the amount of bandwidth this job is expected to
// consume.
func (j *jobRenew) callExpectedBandwidth() (ul, dl uint64) {
	return renewJobExpectedBandwidth()
}

// initJobRenewQueue will initialize a queue for renewing contracts with a host
// for the worker. This is only meant to be run once at startup.
func (w *worker) initJobRenewQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobRenewQueue != nil {
		w.renter.log.Critical("incorret call on initJobRenewQueue")
		return
	}

	w.staticJobRenewQueue = &jobRenewQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}

// RenewContract renews the contract with the worker's host.
func (w *worker) RenewContract(ctx context.Context, fcid types.FileContractID, params modules.ContractParams, txnBuilder modules.TransactionBuilder) (modules.RenterContract, []types.Transaction, error) {
	renewResponseChan := make(chan *jobRenewResponse)
	jro := &jobRenew{
		staticFCID:               fcid,
		staticParams:             params,
		staticResponseChan:       renewResponseChan,
		staticTransactionBuilder: txnBuilder,
		jobGeneric:               newJobGeneric(ctx, w.staticJobReadQueue, nil),
	}

	// Add the job to the queue.
	if !w.staticJobRenewQueue.callAdd(jro) {
		return modules.RenterContract{}, nil, errors.New("worker unavailable")
	}

	// Wait for the response.
	var resp *jobRenewResponse
	select {
	case <-ctx.Done():
		return modules.RenterContract{}, nil, errors.New("Renew interrupted")
	case resp = <-renewResponseChan:
	}
	return resp.staticNewContract, resp.staticTxnSet, resp.staticErr
}
