package renter

import (
	"container/list"
	"context"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
)

var (
	// ErrJobDiscarded is returned by a job if worker conditions have resulted
	// in the worker being able to run this type of job. Perhaps another job of
	// the same type failed recently, or some prerequisite like an ephemeral
	// account refill is not being met. The error may or may not be extended to
	// provide a reason.
	ErrJobDiscarded = errors.New("job is being discarded")
)

type (
	// jobGeneric implements the basic functionality for a job.
	jobGeneric struct {
		staticCtx context.Context

		staticQueue workerJobQueue

		// staticMetadata is a generic field on the job that can be set and
		// casted by implementations of a job
		staticMetadata interface{}

		// These fields are set when the job is added to the job queue and used
		// after execution to log the delta between the estimated job time and
		// the actual job time.
		externJobStartTime         time.Time
		externEstimatedJobDuration time.Duration
	}

	// jobGenericQueue is a generic queue for a job. It has a mutex, references
	// a worker, tracks whether or not it has been killed, and has a cooldown
	// timer. It does not have an array of jobs that are in the queue, because
	// those are type specific.
	jobGenericQueue struct {
		jobs *list.List

		killed bool

		cooldownUntil       time.Time
		consecutiveFailures uint64
		recentErr           error
		recentErrTime       time.Time

		staticWorkerObj *worker // name conflict with staticWorker method
		mu              sync.Mutex
	}

	// workerJob defines a job that the worker is able to perform.
	workerJob interface {
		// callDicard will discard this job, sending an error down the response
		// channel of the job. The provided error should be part of the error
		// that gets sent.
		callDiscard(error)

		// callExecute will run the actual job.
		callExecute()

		// callExpectedBandwidth will return the amount of bandwidth that a job
		// expects to consume.
		callExpectedBandwidth() (upload uint64, download uint64)

		// staticGetMetadata returns a metadata object.
		staticGetMetadata() interface{}

		// staticCanceled returns true if the job has been canceled, false
		// otherwise.
		staticCanceled() bool
	}

	// workerJobQueue defines an interface to create a worker job queue.
	workerJobQueue interface {
		// callDiscardAll will discard all of the jobs in the queue using the
		// provided error.
		callDiscardAll(error)

		// callReportFailure should be called on the queue every time that a job
		// fails, and include the error associated with the failure.
		callReportFailure(error)

		// callReportSuccess should be called on the queue every time that a job
		// succeeds.
		callReportSuccess()

		// callStatus returns the status of the queue
		callStatus() workerJobQueueStatus

		// staticWorker will return the worker of the job queue.
		staticWorker() *worker
	}

	// workerJobQueueStatus is a struct that reflects the status of the queue
	workerJobQueueStatus struct {
		size                uint64
		cooldownUntil       time.Time
		consecutiveFailures uint64
		recentErr           error
		recentErrTime       time.Time
	}
)

// expMovingAvg is a helper to compute the next exponential moving average given
// the last value and a new point of measurement.
// https://en.wikipedia.org/wiki/Moving_average#Exponential_moving_average
func expMovingAvg(oldEMA, newValue, decay float64) float64 {
	if decay < 0 || decay > 1 {
		build.Critical("decay has to be a value in range 0 <= x <= 1")
	}
	return newValue*decay + (1-decay)*oldEMA
}

// newJobGeneric returns an initialized jobGeneric. The queue that is associated
// with the job should be used as the input to this function. The job will
// cancel itself if the cancelChan is closed.
func newJobGeneric(ctx context.Context, queue workerJobQueue, metadata interface{}) *jobGeneric {
	return &jobGeneric{
		staticCtx:      ctx,
		staticQueue:    queue,
		staticMetadata: metadata,
	}
}

// newJobGenericQueue will return an initialized generic job queue.
func newJobGenericQueue(w *worker) *jobGenericQueue {
	return &jobGenericQueue{
		jobs:            list.New(),
		staticWorkerObj: w,
	}
}

// staticCanceled returns whether or not the job has been canceled.
func (j *jobGeneric) staticCanceled() bool {
	select {
	case <-j.staticCtx.Done():
		return true
	default:
		return false
	}
}

// staticGetMetadata returns the job's metadata.
func (j *jobGeneric) staticGetMetadata() interface{} {
	return j.staticMetadata
}

// add will add a job to the queue.
func (jq *jobGenericQueue) add(j workerJob) bool {
	if jq.killed || jq.onCooldown() {
		return false
	}
	jq.jobs.PushBack(j)
	jq.staticWorkerObj.staticWake()
	return true
}

// callAdd will add a job to the queue.
func (jq *jobGenericQueue) callAdd(j workerJob) bool {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return jq.add(j)
}

// callDiscardAll will discard all jobs in the queue using the provided error.
func (jq *jobGenericQueue) callDiscardAll(err error) {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	jq.discardAll(err)
}

// callKill will kill the queue, discarding all jobs and ensuring no more jobs
// can be added.
func (jq *jobGenericQueue) callKill() {
	jq.mu.Lock()
	defer jq.mu.Unlock()

	err := errors.New("worker is being killed")
	jq.discardAll(err)
	jq.killed = true
}

// callLen returns the number of jobs in the queue.
func (jq *jobGenericQueue) callLen() int {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return jq.jobs.Len()
}

// callNext returns the next job in the worker queue. If there is no job in the
// queue, 'nil' will be returned.
func (jq *jobGenericQueue) callNext() workerJob {
	jq.mu.Lock()
	defer jq.mu.Unlock()

	// Loop through the jobs, looking for the first job that hasn't yet been
	// canceled. Remove jobs from the queue along the way.
	for job := jq.jobs.Front(); job != nil; job = job.Next() {
		// Remove the job from the list.
		jq.jobs.Remove(job)

		// Check if the job is already canceled.
		wj := job.Value.(workerJob)
		if wj.staticCanceled() {
			wj.callDiscard(errors.New("callNext: skipping and discarding already canceled job"))
			continue
		}
		return wj
	}

	// Job queue is empty, return nil.
	return nil
}

// callOnCooldown returns whether the queue is on cooldown.
func (jq *jobGenericQueue) callOnCooldown() bool {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return jq.onCooldown()
}

// callReportFailure reports that a job has failed within the queue. This will
// cause all remaining jobs in the queue to be discarded, and will put the queue
// on cooldown.
func (jq *jobGenericQueue) callReportFailure(err error) {
	jq.mu.Lock()
	defer jq.mu.Unlock()

	err = errors.AddContext(err, "discarding all jobs in this queue and going on cooldown")
	jq.discardAll(err)
	jq.cooldownUntil = cooldownUntil(jq.consecutiveFailures)
	jq.consecutiveFailures++
	jq.recentErr = err
	jq.recentErrTime = time.Now()
}

// callReportSuccess lets the job queue know that there was a successsful job.
// Note that this will reset the consecutive failure count, but will not reset
// the recentErr value - the recentErr value is left as an error so that when
// debugging later, developers and users can see what errors had been caused by
// past issues.
func (jq *jobGenericQueue) callReportSuccess() {
	jq.mu.Lock()
	jq.consecutiveFailures = 0
	jq.mu.Unlock()
}

// callStatus returns the queue status
func (jq *jobGenericQueue) callStatus() workerJobQueueStatus {
	jq.mu.Lock()
	defer jq.mu.Unlock()
	return workerJobQueueStatus{
		size:                uint64(jq.jobs.Len()),
		cooldownUntil:       jq.cooldownUntil,
		consecutiveFailures: jq.consecutiveFailures,
		recentErr:           jq.recentErr,
		recentErrTime:       jq.recentErrTime,
	}
}

// discardAll will drop all jobs from the queue.
func (jq *jobGenericQueue) discardAll(err error) {
	for job := jq.jobs.Front(); job != nil; job = job.Next() {
		wj := job.Value.(workerJob)
		wj.callDiscard(err)
	}
	jq.jobs = list.New()
}

// staticWorker will return the worker that is associated with this job queue.
func (jq *jobGenericQueue) staticWorker() *worker {
	return jq.staticWorkerObj
}

// onCooldown returns whether the queue is on cooldown.
func (jq *jobGenericQueue) onCooldown() bool {
	return time.Now().Before(jq.cooldownUntil)
}
