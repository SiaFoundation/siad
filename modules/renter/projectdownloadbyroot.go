package renter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// projectDownloadByRootPerformanceDecay defines the amount of decay that is
	// applied to the exponential weigted average used to compute the
	// performance of the download by root projects that have run recently.
	projectDownloadByRootPerformanceDecay = 0.9
)

var (
	// sectorLookupToDownloadRatio is an arbitrary ratio that resembles the
	// amount of lookups vs downloads. It is used in price gouging checks.
	sectorLookupToDownloadRatio = 16
)

// projectDownloadByRootManager tracks metrics across multiple runs of
// DownloadByRoot projects, and is used by the projects to set expectations for
// performance.
//
// We put downloads into 3 different buckets for performance because the
// performance characterstics are very different depending on which bucket you
// are in.
type projectDownloadByRootManager struct {
	// Aggregate values for download by root projects. These are typically used
	// for research purposes, as opposed to being used in real time.
	totalTime64k     time.Duration
	totalTime1m      time.Duration
	totalTime4m      time.Duration
	totalRequests64k uint64
	totalRequests1m  uint64
	totalRequests4m  uint64

	// Decayed values track the recent performance of jobs in each bucket. These
	// values are generally used to help select workers when scheduling work,
	// because they are more responsive to changing network conditions.
	decayedTime64k     float64
	decayedTime1m      float64
	decayedTime4m      float64
	decayedRequests64k float64
	decayedRequests1m  float64
	decayedRequests4m  float64

	mu sync.Mutex
}

// managedRecordProjectTime adds a download to the historic values of the
// project manager. It takes a length so that it knows which bucket to put the
// data in.
func (m *projectDownloadByRootManager) managedRecordProjectTime(length uint64, timeElapsed time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if length <= 1<<16 {
		m.totalTime64k += timeElapsed
		m.totalRequests64k++
		m.decayedTime64k *= projectDownloadByRootPerformanceDecay
		m.decayedRequests64k *= projectDownloadByRootPerformanceDecay
		m.decayedTime64k += float64(timeElapsed)
		m.decayedRequests64k++
	} else if length <= 1<<20 {
		m.totalTime1m += timeElapsed
		m.totalRequests1m++
		m.decayedTime1m *= projectDownloadByRootPerformanceDecay
		m.decayedRequests1m *= projectDownloadByRootPerformanceDecay
		m.decayedTime1m += float64(timeElapsed)
		m.decayedRequests1m++
	} else {
		m.totalTime4m += timeElapsed
		m.totalRequests4m++
		m.decayedTime4m *= projectDownloadByRootPerformanceDecay
		m.decayedRequests4m *= projectDownloadByRootPerformanceDecay
		m.decayedTime4m += float64(timeElapsed)
		m.decayedRequests4m++
	}
}

// managedAverageProjectTime will return the average download time that projects
// have had for the given length.
func (m *projectDownloadByRootManager) managedAverageProjectTime(length uint64) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	var avg time.Duration
	if length <= 1<<16 {
		avg = time.Duration(m.decayedTime64k / m.decayedRequests64k)
	} else if length <= 1<<20 {
		avg = time.Duration(m.decayedTime1m / m.decayedRequests1m)
	} else {
		avg = time.Duration(m.decayedTime4m / m.decayedRequests4m)
	}
	return avg
}

// managedDownloadByRoot will fetch data using the merkle root of that data.
// Unlike the exported version of this function, this function does not request
// memory from the memory manager.
func (r *Renter) managedDownloadByRoot(ctx context.Context, root crypto.Hash, offset, length uint64) ([]byte, error) {
	// Create a context that dies when the function ends, this will cancel all
	// of the worker jobs that get created by this function.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Convenience variable.
	pm := r.staticProjectDownloadByRootManager
	// Track the total duration of the project.
	start := time.Now()

	// Potentially force a timeout via a disrupt for testing.
	if r.deps.Disrupt("timeoutProjectDownloadByRoot") {
		return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
	}

	// Get the full list of workers and create a channel to receive all of the
	// results from the workers. The channel is buffered with one slot per
	// worker, so that the workers do not have to block when returning the
	// result of the job, even if this thread is not listening.
	workers := r.staticWorkerPool.callWorkers()
	staticResponseChan := make(chan *jobHasSectorResponse, len(workers))

	// Filter out all workers that do not support the new protocol. It has been
	// determined that hosts who do not support the async protocol are not worth
	// supporting in the new download by root code - it'll remove pretty much
	// all of the performance advantages. Skynet is being forced to fully
	// migrate to the async protocol.
	numAsyncWorkers := 0
	for _, worker := range workers {
		cache := worker.staticCache()
		if build.VersionCmp(cache.staticHostVersion, minAsyncVersion) < 0 {
			continue
		}

		// check for price gouging
		pt := worker.staticPriceTable().staticPriceTable
		err := checkPDBRGouging(pt, cache.staticRenterAllowance)
		if err != nil {
			r.log.Debugf("price gouging detected in worker %v, err: %v\n", worker.staticHostPubKeyStr, err)
			continue
		}

		jhs := &jobHasSector{
			staticSector:       root,
			staticResponseChan: staticResponseChan,

			jobGeneric: &jobGeneric{
				staticCancelChan: ctx.Done(),

				staticQueue: worker.staticJobHasSectorQueue,
			},
		}
		if !worker.staticJobHasSectorQueue.callAdd(jhs) {
			// This will filter out any workers that are on cooldown or
			// otherwise can't participate in the project.
			continue
		}
		workers[numAsyncWorkers] = worker
		numAsyncWorkers++
	}
	workers = workers[:numAsyncWorkers]
	// If there are no workers remaining, fail early.
	if len(workers) == 0 {
		return nil, errors.New("cannot perform DownloadByRoot, no workers in worker pool")
	}

	// Create a timer that is used to determine when the project should stop
	// looking for a better worker, and instead go use the best worker it has
	// found so far.
	//
	// Currently, we track the recent historical performance of projects using
	// an exponential weighted average. Workers also track their recent
	// performance using an exponential weighted average. Using these two
	// values, we can determine whether using a worker is likely to result in
	// better than historic average performance.
	//
	// If a worker does look like it can be used to achieve better than average
	// performance, we will use that worker immediately. Otherwise, we will wait
	// for a better worker to appear.
	//
	// After we have spent half of the whole historic time waiting for better
	// workers to appear, we give up and use the best worker that we have found
	// so far.
	useBestWorkerCtx, useBestWorkerCancel := context.WithTimeout(ctx, pm.managedAverageProjectTime(length)/2)
	defer useBestWorkerCancel()

	// Run a loop to receive responses from the workers as they figure out
	// whether or not they have the sector we are looking for. The loop needs to
	// run until we have tried every worker, which means that the number of
	// responses must be equal to the number of workers, and the length of the
	// usable workers map must be 0.
	//
	// The usable workers map is a map from the iteration that we found the
	// worker to the worker. We use a map because it makes it easy to see the
	// length, is simple enough to implement, and iterating over a whole map
	// with 30 or so elements in it is not too costly. It is also easy to delete
	// elements from a map as workers fail.
	responses := 0
	usableWorkers := make(map[int]*worker)
	useBestWorker := false
	for responses < len(workers) || len(usableWorkers) > 0 {
		// Check for the timeout. This is done separately to ensure the timeout
		// has priority.
		select {
		case <-ctx.Done():
			return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
		default:
		}

		var resp *jobHasSectorResponse
		if len(usableWorkers) > 0 && responses < numAsyncWorkers {
			// There are usable workers, and there are also workers that have
			// not reported back yet. Because we have usable workers, we want to
			// listen on the useBestWorkerChan.
			select {
			case <-useBestWorkerCtx.Done():
				useBestWorker = true
			case resp = <-staticResponseChan:
				responses++
			case <-ctx.Done():
				return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
			}
		} else if len(usableWorkers) == 0 {
			// There are no usable workers, which means there's no point
			// listening on the useBestWorkerChan.
			select {
			case resp = <-staticResponseChan:
				responses++
			case <-ctx.Done():
				return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
			}
		} else {
			// All workers have responded, which means we should now use the
			// best worker that we have to attempt the download. No need to wait
			// for a signal.
			useBestWorker = true
		}

		// If we received a response from a worker that is not useful for
		// completing the project, go back to blocking. This check is ignored if
		// we are supposed to use the best worker.
		if (resp == nil || resp.staticErr != nil || !resp.staticAvailable) && !useBestWorker {
			continue
		}

		// If there was a positive response, add this worker to the set of
		// usable workers. Check whether or not this worker is expected to
		// finish better than the average project time. If so, set a flag so
		// that the download continues even if we aren't yet ready to use the
		// best known worker.
		goodEnough := false
		if resp != nil && resp.staticErr == nil && resp.staticAvailable {
			w := resp.staticWorker
			jq := w.staticJobReadQueue
			usableWorkers[responses] = w
			goodEnough = time.Since(start)+jq.callAverageJobTime(length) < pm.managedAverageProjectTime(length)
		}

		// Determine whether to move forward with the download or wait for more
		// workers. If the useBestWorker flag is set, we will move forward with
		// the download. If the most recent worker has an average job time that
		// would expect us to complete this job faster than usual, we can move
		// forward with that worker.
		//
		// This conditional is  set up as an inverse so that we can continue
		// rather than putting all of the logic inside a big if block.
		if !useBestWorker && !goodEnough {
			continue
		}
		// If there are no usable workers, continue.
		if len(usableWorkers) == 0 {
			continue
		}

		// Scan through the set of workers to find the best worker.
		var bestWorkerIndex int
		var bestWorker *worker
		var bestWorkerTime time.Duration
		for i, w := range usableWorkers {
			wTime := w.staticJobReadQueue.callAverageJobTime(length)
			if bestWorkerTime == 0 || wTime < bestWorkerTime {
				bestWorkerTime = wTime
				bestWorkerIndex = i
				bestWorker = w
			}
		}
		// Delete this worker from the set of usable workers, because if this
		// download fails, the worker shouldn't be used again.
		delete(usableWorkers, bestWorkerIndex)

		// Queue the job to download the sector root.
		readSectorRespChan := make(chan *jobReadResponse)
		jrs := &jobReadSector{
			jobRead: jobRead{
				staticResponseChan: readSectorRespChan,
				staticLength:       length,
				jobGeneric: &jobGeneric{
					staticCancelChan: ctx.Done(),

					staticQueue: bestWorker.staticJobReadQueue,
				},
			},
			staticOffset: offset,
			staticSector: root,
		}
		if !bestWorker.staticJobReadQueue.callAdd(jrs) {
			continue
		}

		// Wait for a response from the worker.
		//
		// TODO: This worker is currently a single point of failure, if the
		// worker takes longer to respond than the lookup timeout, the project
		// will fail even though there are potentially more workers to be using.
		// I think the best way to fix this is to swich to the multi-worker
		// paradigm, where we use multiple workers to fetch a single sector
		// root.
		var readSectorResp *jobReadResponse
		select {
		case readSectorResp = <-readSectorRespChan:
		case <-ctx.Done():
			return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
		}

		// If the read sector job was not successful, move on to the next
		// worker.
		if readSectorResp == nil || readSectorResp.staticErr != nil {
			continue
		}

		// We got a good response! Record the total project time and return the
		// data.
		pm.managedRecordProjectTime(length, time.Since(start))
		return readSectorResp.staticData, nil
	}

	// All workers have failed.
	return nil, ErrRootNotFound
}

// DownloadByRoot will fetch data using the merkle root of that data. This uses
// all of the async worker primitives to improve speed and throughput.
func (r *Renter) DownloadByRoot(root crypto.Hash, offset, length uint64, timeout time.Duration) ([]byte, error) {
	// Block until there is memory available, and then ensure the memory gets
	// returned.
	if !r.memoryManager.Request(length, memoryPriorityHigh) {
		return nil, errors.New("renter shut down before memory could be allocated for the project")
	}
	defer r.memoryManager.Return(length)

	// Create a context. If the timeout is greater than zero, have the context
	// expire when the timeout triggers.
	ctx := r.tg.StopCtx()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(r.tg.StopCtx(), timeout)
		defer cancel()
	}

	data, err := r.managedDownloadByRoot(ctx, root, offset, length)
	if errors.Contains(err, ErrProjectTimedOut) {
		err = errors.AddContext(err, fmt.Sprintf("timed out after %vs", timeout.Seconds()))
	}
	return data, err
}

// checkPDBRGouging verifies the cost of executing the jobs performed by the
// PDBR are reasonable in relation to the user's allowance and the amount of
// data he intends to download
func checkPDBRGouging(pt modules.RPCPriceTable, allowance modules.Allowance) error {
	// Check whether the download bandwidth price is too high.
	if !allowance.MaxDownloadBandwidthPrice.IsZero() && allowance.MaxDownloadBandwidthPrice.Cmp(pt.DownloadBandwidthCost) < 0 {
		return fmt.Errorf("download bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v - price gouging protection enabled", pt.DownloadBandwidthCost, allowance.MaxDownloadBandwidthPrice)
	}

	// Check whether the upload bandwidth price is too high.
	if !allowance.MaxUploadBandwidthPrice.IsZero() && allowance.MaxUploadBandwidthPrice.Cmp(pt.UploadBandwidthCost) < 0 {
		return fmt.Errorf("upload bandwidth price of host is %v, which is above the maximum allowed by the allowance: %v - price gouging protection enabled", pt.UploadBandwidthCost, allowance.MaxUploadBandwidthPrice)
	}

	// If there is no allowance, price gouging checks have to be disabled,
	// because there is no baseline for understanding what might count as price
	// gouging.
	if allowance.Funds.IsZero() {
		return nil
	}

	// In order to decide whether or not the cost of performing a PDBR is too
	// expensive, we make some assumptions with regards to lookup vs download
	// job ratio and avg download size. The total cost is then compared in
	// relation to the allowance, where we verify that a fraction of the cost
	// (which we'll call reduced cost) to download the amount of data the user
	// intends to download does not exceed its allowance.

	// Calculate the cost of a has sector job
	pb := modules.NewProgramBuilder(&pt, 0)
	pb.AddHasSectorInstruction(crypto.Hash{})
	programCost, _, _ := pb.Cost(true)

	ulbw, dlbw := hasSectorJobExpectedBandwidth()
	bandwidthCost := modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costHasSectorJob := programCost.Add(bandwidthCost)

	// Calculate the cost of a read sector job, we use StreamDownloadSize as an
	// average download size here which is 64 KiB.
	pb = modules.NewProgramBuilder(&pt, 0)
	pb.AddReadSectorInstruction(modules.StreamDownloadSize, 0, crypto.Hash{}, true)
	programCost, _, _ = pb.Cost(true)

	ulbw, dlbw = readSectorJobExpectedBandwidth(modules.StreamDownloadSize)
	bandwidthCost = modules.MDMBandwidthCost(pt, ulbw, dlbw)
	costReadSectorJob := programCost.Add(bandwidthCost)

	// Calculate the cost of a project
	costProject := costReadSectorJob.Add(costHasSectorJob.Mul64(uint64(sectorLookupToDownloadRatio)))

	// Now that we have the cost of each job, and we estimate a sector lookup to
	// download ratio of 16, all we need is calculate the number of projects
	// necesstaryo download the expected download amount.
	numProjects := allowance.ExpectedDownload / modules.StreamDownloadSize

	// The cost of downloading is considered too expensive if the allowance is
	// insufficient to cover a fraction of the expense to download the amount of
	// data the user intends to download
	totalCost := costProject.Mul64(numProjects)
	reducedCost := totalCost.Div64(downloadGougingFractionDenom)
	if reducedCost.Cmp(allowance.Funds) > 0 {
		errStr := fmt.Sprintf("combined PDBR pricing of host yields %v, which is more than the renter is willing to pay for downloads: %v - price gouging protection enabled", reducedCost, allowance.Funds)
		return errors.New(errStr)
	}

	return nil
}
