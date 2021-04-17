package renter

import (
	"math"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/types"
)

const (
	// maxExpBackoffJitterMS defines the maximum number of milliseconds that can
	// get added as jitter to the wait time in the exponential backoff
	// mechanism.
	maxExpBackoffJitterMS = 100

	// maxExpBackoffDelayMS defines the maximum number of milliseconds that can
	// be induced by the exponential backoff mechanism.
	maxExpBackoffDelayMS = 3000

	// maxExpBackoffRetryCount defines at what retry count the exponential
	// backoff mechanism defaults to maxExpBackoffDelayMS. At current values
	// this is set to 12 as that would induce a minimum wait of over 4s, which
	// is higher than the current maxExpBackoffDelayMS.
	maxExpBackoffRetryCount = 12
)

// TODO: Better handling of time.After

// TODO: The pricing mechanism for these overdrive workers is not optimal
// because the pricing mechanism right now assumes there is only one overdrive
// worker and that the overdrive worker definitely is the slowest/latest worker
// out of everyone that has already launched. For the most part, these
// assumptions are going to be true in 99% of cases, so this doesn't need to be
// addressed immediately.

// adjustedReadDuration returns the amount of time that a worker is expected to
// take to return, taking into account the penalties for the price of the
// download and a potential cooldown on the read queue.
func (pdc *projectDownloadChunk) adjustedReadDuration(w *worker) time.Duration {
	jrq := w.staticJobReadQueue

	// Fetch the expected job time.
	jobTime := jrq.callExpectedJobTime(pdc.pieceLength)
	if jobTime < 0 {
		jobTime = 0
	}

	// If the queue is on cooldown, add the remaining cooldown period.
	if jrq.callOnCooldown() {
		jrq.mu.Lock()
		jobTime = jobTime + time.Until(jrq.cooldownUntil)
		jrq.mu.Unlock()
	}

	// Add a penalty to performance based on the cost of the job.
	jobCost := jrq.callExpectedJobCost(pdc.pieceLength)
	return addCostPenalty(jobTime, jobCost, pdc.pricePerMS)
}

// bestOverdriveUnresolvedWorker will scan through a proveded list of unresolved
// workers and find the best one to use as an overdrive worker.
//
// Four values are returned.
//
// The first indicates whether an unresolved worker was found that beats the
// initial duration, which is pessimistically set to math.MaxInt64. Due to job
// cost however an unresolved worker might not beat that value.
//
// The second signifies whether the best worker is late. If so, any worker that
// is resolved should be preferred over any worker that is unresolved.
//
// The third return value is the unresolved duration. This is a modified
// duration based on the combination of the amount of time until the worker has
// completed its task plus the amount of time penalty the worker incurs for
// being expensive.
//
// The final return value is a wait duration, which indicates how much time
// needs to elapse before the best unresolved worker flips over into being a
// late worker.
func (pdc *projectDownloadChunk) bestOverdriveUnresolvedWorker(puws []*pcwsUnresolvedWorker) (exists, late bool, duration, waitDuration time.Duration, workerIndex int) {
	// Set the duration and late status to the most pessimistic value.
	exists = false
	late = true
	duration = time.Duration(math.MaxInt64)
	waitDuration = time.Duration(math.MaxInt64)
	workerIndex = -1

	// Loop through the unresovled workers and find the best unresolved worker.
	for i, uw := range puws {
		// Figure how much time is expected to remain until the worker is
		// available. Note that no price penalty is attached to the HasSector
		// call, because that call is being made regardless of the cost.
		uwLate := false
		hasSectorTime := time.Until(uw.staticExpectedResolvedTime)
		if hasSectorTime < 0 {
			hasSectorTime = 0
			uwLate = true
		}
		// Skip this worker if the best is not late but this worker is late.
		if uwLate && !late {
			continue
		}

		// Figure out how much time is expected until the worker completes the
		// download job.
		readTime := pdc.adjustedReadDuration(uw.staticWorker)

		// Ensure we don't overflow
		var adjustedTotalDuration time.Duration
		if readTime > math.MaxInt64-hasSectorTime {
			adjustedTotalDuration = math.MaxInt64
		} else {
			adjustedTotalDuration = hasSectorTime + readTime
		}

		// Compare the total time (including price preference) to the current
		// best time. Workers that are not late get preference over workers that
		// are late.
		betterLateStatus := !uwLate && late
		betterDuration := adjustedTotalDuration < duration
		if betterLateStatus || betterDuration {
			exists = true
			duration = adjustedTotalDuration
			workerIndex = i
			if !uwLate {
				waitDuration = hasSectorTime
				late = false
			}
		}
	}
	return exists, late, duration, waitDuration, workerIndex
}

// findBestOverdriveWorker will search for the best worker to contribute to an
// overdrive. The selection criteria is to select a worker that is expected to
// be the fastest. If the fastest worker is an unresolved worker, the worker
// return value will be 'nil' and instead two channels will be returned which
// help to indicate when the unresolved worker is resolved.
//
// The time.Duration indicates how long until the preferred worker would be
// late, and the channel will be closed when new worker updates are available.
//
// TODO: Remember the edge case where all unresolved workers have not returned
// yet and there are no other options. There is no timer in that case, only
// blocking on workersUpdatedChan.
func (pdc *projectDownloadChunk) findBestOverdriveWorker() (*worker, uint64, <-chan struct{}, <-chan time.Time) {
	// Find the best unresolved worker. The return values include an 'adjusted
	// duration', which indicates how long the worker takes accounting for
	// pricing, and the 'wait duration', which is the max amount of time that we
	// should wait for this worker to return with a result from HasSector.
	//
	// If the best unresolved worker is late, the wait duration will be zero.
	// Technically these values are redundant but the code felt cleaner to
	// return them explicitly.
	//
	// buw = bestUnresolvedWorker
	unresolvedWorkers, updateChan := pdc.unresolvedWorkers()
	buwExists, buwLate, buwAdjustedDuration, buwWaitDuration, _ := pdc.bestOverdriveUnresolvedWorker(unresolvedWorkers)

	// Loop through the set of pieces to find the fastest worker that can be
	// launched. Because this function is only called for overdrive workers, we
	// can assume that any launched piece is already late.
	//
	// baw = bestAvailableWorker
	bawAdjustedDuration := time.Duration(math.MaxInt64)
	bawPieceIndex := 0
	var baw *worker

	for i, activePiece := range pdc.availablePieces {
		for _, pieceDownload := range activePiece {
			// Don't consider any workers from this piece if the piece is
			// completed.
			if pieceDownload.completed {
				break
			}

			// Skip over failed pieces or pieces that have already launched.
			if pieceDownload.downloadErr != nil || pieceDownload.launched {
				continue
			}

			// Determine if this worker is better than any existing worker.
			workerAdjustedDuration := pdc.adjustedReadDuration(pieceDownload.worker)
			if workerAdjustedDuration < bawAdjustedDuration {
				bawAdjustedDuration = workerAdjustedDuration
				bawPieceIndex = i
				baw = pieceDownload.worker
			}
		}
	}

	// Return nil if there are no workers that can be launched.
	if !buwExists && baw == nil {
		// All 'nil' return values, meaning the download can succeed by waiting
		// for already launched workers to return, but cannot succeed by
		// launching new workers because no new workers are available.
		return nil, 0, nil, nil
	}

	// Return the buw values unconditionally if there is no baw. Also return the
	// buw if the buw is not late and has a better duration than the baw.
	buwNoBaw := buwExists && baw == nil
	buwBetter := !buwLate && buwAdjustedDuration < bawAdjustedDuration
	if buwNoBaw || buwBetter {
		return nil, 0, updateChan, time.After(buwWaitDuration)
	}

	// Return the baw.
	return baw, uint64(bawPieceIndex), nil, nil
}

// tryLaunchOverdriveWorker will attempt to launch an overdrive worker. A worker
// may not be launched if the best worker is not yet available.
//
// If a worker was launched successfully, the expected return time of that
// worker will be returned. If a worker was not successful, a 'wakeChan' will be
// returned which indicates an update to the worker state, and a time.After()
// will be returned which indicates when the worker flips over to being late and
// therefore another worker should be selected.
func (pdc *projectDownloadChunk) tryLaunchOverdriveWorker() (bool, time.Time, <-chan struct{}, <-chan time.Time) {
	// Loop until either a launch succeeds or until the best worker is not
	// found.
	retry := 0
	for {
		worker, pieceIndex, wakeChan, workerLateChan := pdc.findBestOverdriveWorker()
		if worker == nil {
			return false, time.Time{}, wakeChan, workerLateChan
		}

		// If there was a worker found, launch the worker.
		expectedReturnTime, success := pdc.launchWorker(worker, pieceIndex, true)
		if !success {
			// If we were unable to successfully launch the worker, we retry
			// after a certain delay. This to prevent spamming the readqueue
			// with jobs in case the queue is on a cooldown.
			select {
			case <-pdc.workerSet.staticRenter.tg.StopChan():
				return false, time.Time{}, wakeChan, workerLateChan
			case <-time.After(expBackoffDelayMS(retry)):
				retry++
				continue
			}
		}

		return true, expectedReturnTime, nil, nil
	}
}

// overdriveStatus will return the number of overdrive workers that need to be
// launched, and the expected return time of the slowest worker that has already
// launched a download task.
func (pdc *projectDownloadChunk) overdriveStatus() (int, time.Time) {
	// Go through the pieces, determining how many pieces are launched without
	// fail, and what the latest return time is of all the workers that have
	// already been launched.
	numLWF := 0 // LWF = launchedWithoutFail
	var latestReturn time.Time
	for _, piece := range pdc.availablePieces {
		launchedWithoutFail := false
		for _, pieceDownload := range piece {
			if !pieceDownload.launched || pieceDownload.downloadErr != nil {
				continue // skip
			}
			launchedWithoutFail = true
			if !pieceDownload.completed && pieceDownload.expectedCompleteTime.After(latestReturn) {
				latestReturn = pieceDownload.expectedCompleteTime
			}
		}
		if launchedWithoutFail {
			numLWF++
		}
	}

	// If there are not enough LWF workers to complete the download, return the
	// number of workers that need to launch in order to complete the download.
	workersWanted := pdc.workerSet.staticErasureCoder.MinPieces()
	if numLWF < workersWanted {
		return workersWanted - numLWF, latestReturn
	}

	// If the latest worker should have already completed its job, return that
	// an overdrive worker should be launched.
	if time.Now().After(latestReturn) {
		return 1, latestReturn
	}

	// If the latest worker is expected to return at some point in the future,
	// there is no immediate need to launch an overdrive worker.
	return 0, latestReturn
}

// tryOverdrive will determine whether an overdrive worker needs to be launched.
// If so, it will launch an overdrive worker asynchronously. It will return two
// channels, one of which will fire when tryOverdrive should be called again. If
// there are no more overdrive workers to try, these channels may both be 'nil'
// and therefore will never fire.
func (pdc *projectDownloadChunk) tryOverdrive() (<-chan struct{}, <-chan time.Time) {
	// Fetch the number of overdrive workers that are needed, and the latest
	// return time of any active worker.
	neededOverdriveWorkers, latestReturn := pdc.overdriveStatus()

	// Launch all of the workers that are needed. If at any point a launch
	// fails, return the status channels to try again.
	for i := 0; i < neededOverdriveWorkers; i++ {
		// If a worker is launched successfully, we care about the expected
		// return time of that worker. Otherwise, we care about wakeChan and
		// expectedReadyChan, one of which will fire when the next overdrive
		// worker is ready. If there are no more overdrive workers, these
		// channels will be nil and therefore never fire.
		workerLaunched, expectedReturnTime, wakeChan, expectedReadyChan := pdc.tryLaunchOverdriveWorker()
		if !workerLaunched {
			return wakeChan, expectedReadyChan
		}

		// Worker launched successfully, update the latestReturnTime to account
		// for the new worker.
		if expectedReturnTime.After(latestReturn) {
			latestReturn = expectedReturnTime
		}
	}

	// All needed overdrive workers have been launched. No need to try again
	// until the current set of workers are late.
	return nil, time.After(time.Until(latestReturn))
}

// addCostPenalty takes a certain job time and adds a penalty to it depending on
// the jobcost and the pdc's price per MS.
func addCostPenalty(jobTime time.Duration, jobCost, pricePerMS types.Currency) time.Duration {
	// If the pricePerMS is higher or equal than the cost of the job, simply
	// return without penalty.
	if pricePerMS.Cmp(jobCost) >= 0 {
		return jobTime
	}

	// Otherwise, add a penalty
	var adjusted time.Duration
	penalty, err := jobCost.Div(pricePerMS).Uint64()
	if err != nil || penalty > math.MaxInt64 {
		adjusted = time.Duration(math.MaxInt64)
	} else if reduced := math.MaxInt64 - int64(penalty); int64(jobTime) > reduced {
		adjusted = time.Duration(math.MaxInt64)
	} else {
		adjusted = jobTime + (time.Duration(penalty) * time.Millisecond)
	}
	return adjusted
}

// expBackoffDelayMS is a helper function that implements a very rudimentary
// exponential backoff delay capped at 3s.
func expBackoffDelayMS(retry int) time.Duration {
	maxDelayDur := time.Duration(maxExpBackoffDelayMS) * time.Millisecond

	// seeing as a retry of 12 guarantees a delay of 3s, return the max delay,
	// this also prevents an overflow for large retry values
	if retry > maxExpBackoffRetryCount {
		return maxDelayDur
	}

	delayMS := int(math.Pow(2, float64(retry)))
	delayMS += fastrand.Intn(maxExpBackoffJitterMS) // 100ms jitter
	if delayMS > maxExpBackoffDelayMS {
		return maxDelayDur
	}
	return time.Duration(delayMS) * time.Millisecond
}
