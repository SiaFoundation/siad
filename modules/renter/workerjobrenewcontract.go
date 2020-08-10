package renter

import (
	"gitlab.com/NebulousLabs/Sia/crypto"

	"gitlab.com/NebulousLabs/errors"
)

type (
	jobRenew struct {
		staticResponseChan chan *jobRenewResponse // Channel to send a response down

		*jobGeneric
	}

	jobRenewQueue struct {
		*jobGenericQueue
	}

	jobRenewResponse struct {
		staticErr error

		// The worker is included in the response so that the caller can listen
		// on one channel for a bunch of workers and still know which worker
		// successfully found the sector root.
		staticWorker *worker
	}
)

// TODO: Gouging

// newJobHasSector is a helper method to create a new HasSector job.
func (w *worker) newJobRenew(cancel <-chan struct{}, responseChan chan *jobRenewResponse, roots ...crypto.Hash) *jobRenew {
	return &jobRenew{
		staticResponseChan: responseChan,
		jobGeneric:         newJobGeneric(w.staticJobHasSectorQueue, cancel),
	}
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
		case <-j.staticCancelChan:
		case <-w.renter.tg.StopChan():
		}
	})
}

// callExecute will run the renew job.
func (j *jobRenew) callExecute() {
	w := j.staticQueue.staticWorker()
	err := w.managedRenew()

	// Send the response.
	response := &jobRenewResponse{
		staticErr: err,

		staticWorker: w,
	}
	w.renter.tg.Launch(func() {
		select {
		case j.staticResponseChan <- response:
		case <-j.staticCancelChan:
		case <-w.renter.tg.StopChan():
		}
	})

	// Report success or failure to the queue.
	if err == nil {
		j.staticQueue.callReportSuccess()
	} else {
		j.staticQueue.callReportFailure(err)
		return
	}
}

func (w *worker) initJobRenewQueue() {
	// Sanity check that there is no existing job queue.
	if w.staticJobRenewQueue != nil {
		w.renter.log.Critical("incorret call on initJobRenewQueue")
		return
	}

	w.staticJobHasSectorQueue = &jobHasSectorQueue{
		jobGenericQueue: newJobGenericQueue(w),
	}
}
