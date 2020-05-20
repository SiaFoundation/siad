package renter

import (
	"fmt"
	"sync"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"

	"gitlab.com/NebulousLabs/errors"
)

const (
	// projectDownloadByRootPerformanceDecay defines the amount of decay that is
	// applied to the exponential weigted average used to compute the
	// performance of the download by root projects that have run recently.
	projectDownloadByRootPerformanceDecay = 0.9
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
	totalTime64k   time.Duration
	totalTime1m    time.Duration
	totalTime4m    time.Duration
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
	var bucket uint64
	var recentAvg time.Duration
	var totalAvg time.Duration
	var totalRequests uint64
	m.mu.Lock()
	if length <= 1 << 16 {
		m.totalTime64k += timeElapsed
		m.totalRequests64k++
		m.decayedTime64k *= projectDownloadByRootPerformanceDecay
		m.decayedRequests64k *= projectDownloadByRootPerformanceDecay
		m.decayedTime64k += float64(timeElapsed)
		m.decayedRequests64k++
		bucket = 1 << 16
		recentAvg = time.Duration(m.decayedTime64k / m.decayedRequests64k)
		totalAvg = m.totalTime64k / time.Duration(m.totalRequests64k)
		totalRequests = m.totalRequests64k
	} else if length <= 1 << 20 {
		m.totalTime1m += timeElapsed
		m.totalRequests1m++
		m.decayedTime1m *= projectDownloadByRootPerformanceDecay
		m.decayedRequests1m *= projectDownloadByRootPerformanceDecay
		m.decayedTime1m += float64(timeElapsed)
		m.decayedRequests1m++
		bucket = 1 << 20
		recentAvg = time.Duration(m.decayedTime1m / m.decayedRequests1m)
		totalAvg = m.totalTime1m / time.Duration(m.totalRequests1m)
		totalRequests = m.totalRequests1m
	} else {
		m.totalTime4m += timeElapsed
		m.totalRequests4m++
		m.decayedTime4m *= projectDownloadByRootPerformanceDecay
		m.decayedRequests4m *= projectDownloadByRootPerformanceDecay
		m.decayedTime4m += float64(timeElapsed)
		m.decayedRequests4m++
		bucket = 1 << 22
		recentAvg = time.Duration(m.decayedTime4m / m.decayedRequests4m)
		totalAvg = m.totalTime4m / time.Duration(m.totalRequests4m)
		totalRequests = m.totalRequests4m
	}
	m.mu.Unlock()
	fmt.Printf("Bucket %v has had recent performance %v, and historic performance %v over %v requests\n", bucket, recentAvg, totalAvg, totalRequests)
}

// mangedAverageProjectTime will return the average download time that prjects
// have had for the given length.
func (m *projectDownloadByRootManager) managedAverageProjectTime(length uint64) time.Duration {
	var avg time.Duration
	m.mu.Lock()
	if length <= 1 << 16 {
		avg = time.Duration(m.decayedTime64k / m.decayedRequests64k)
	} else if length <= 1 << 20 {
		avg = time.Duration(m.decayedTime1m / m.decayedRequests1m)
	} else {
		avg = time.Duration(m.decayedTime4m / m.decayedRequests4m)
	}
	m.mu.Unlock()
	return avg
}

// managedDownloadByRoot will fetch data using the merkle root of that data.
// Unlike the exported version of this function, this function does not request
// memory from the memory manager.
func (r *Renter) managedDownloadByRoot(root crypto.Hash, offset, length uint64, timeout time.Duration) ([]byte, error) {
	// Convenience variable.
	pm := r.staticProjectDownloadByRootManager
	// Track the total duration of the project.
	start := time.Now()

	// Potentially force a timeout via a disrupt for testing.
	if r.deps.Disrupt("timeoutProjectDownloadByRoot") {
		return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
	}

	// Create a channel to time out the project. Use a nil channel if the
	// timeout is zero, so that the timeout never fires.
	var timeoutChan <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		timeoutChan = timer.C

		// Defer a function to clean up the timer so nothing else in the
		// function needs to worry about it.
		defer func() {
			if !timer.Stop() {
				<-timer.C
			}
		}()
	}

	// Create a channel to signal to workers when the job has been completed.
	// This will cause any workers who have not yet started the job to ignore it
	// instead of doing duplicate work.
	cancelChan := make(chan struct{})
	defer func() {
		// Automatically cancel the work when the function exits.
		close(cancelChan)
	}()

	// Get the full list of workers that could potentially download the root.
	workers := r.staticWorkerPool.callWorkers()
	if len(workers) == 0 {
		return nil, errors.New("cannot perform DownloadByRoot, no workers in worker pool")
	}

	// Create a channel to receive all of the results from the workers. The
	// channel is buffered with one slot per worker, so that the workers do not
	// have to block when returning the result of the job, even if this thread
	// is not listening.
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
		jhs := jobHasSector{
			staticCanceledChan: cancelChan,
			staticSector:       root,
			staticResponseChan: staticResponseChan,
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
	useBestWorkerChan := make(chan struct{})
	useBestWorkerTimer := time.AfterFunc(pm.managedAverageProjectTime(length)/2, func() {
		close(useBestWorkerChan)
	})
	// Clean up the timer.
	defer func() {
		useBestWorkerTimer.Stop()
	}()

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
		case <-timeoutChan:
			return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
		default:
		}

		var resp *jobHasSectorResponse
		if len(usableWorkers) > 0 && responses < numAsyncWorkers {
			// There are usable workers, and there are also workers that have
			// not reported back yet. Because we have usable workers, we want to
			// listen on the useBestWorkerChan.
			select {
			case <-useBestWorkerChan:
				useBestWorker = true
			case resp = <-staticResponseChan:
				responses++
			case <-timeoutChan:
				return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
			}
		} else if len(usableWorkers) == 0 {
			// There are no usable workers, which means there's no point
			// listening on the useBestWorkerChan.
			select {
			case resp = <-staticResponseChan:
				responses++
			case <-timeoutChan:
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
			jq := w.staticJobReadSectorQueue
			usableWorkers[responses] = w
			goodEnough = time.Since(start) + jq.callAverageJobTime(length) < pm.managedAverageProjectTime(length)
			fmt.Printf("%v: HasSector positive response received: %v\n", w.staticHostPubKeyStr, time.Since(start))
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
			wTime := w.staticJobReadSectorQueue.callAverageJobTime(length)
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
		readSectorRespChan := make(chan *jobReadSectorResponse)
		jrs := jobReadSector{
			staticCanceledChan: cancelChan,
			staticResponseChan: readSectorRespChan,

			staticLength: length,
			staticOffset: offset,
			staticSector: root,
		}
		if !bestWorker.staticJobReadSectorQueue.callAdd(jrs) {
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
		var readSectorResp *jobReadSectorResponse
		select {
		case readSectorResp = <-readSectorRespChan:
		case <-timeoutChan:
			return nil, errors.Compose(ErrProjectTimedOut, ErrRootNotFound)
		}

		// If the read sector job was not successful, move on to the next
		// worker.
		if readSectorResp == nil || readSectorResp.staticErr != nil {
			fmt.Printf("%v: Sector data fetch failed: %v\n", root, time.Since(start))
			continue
		}

		// We got a good response! Record the total project time and return the
		// data.
		pm.managedRecordProjectTime(length, time.Since(start))
		fmt.Printf("%v: Sector data received: %v\n", root, time.Since(start))
		return readSectorResp.staticData, nil
	}

	// All workers have failed.
	fmt.Println("Not found, all workers have failed.")
	return nil, ErrRootNotFound
}

// DownloadByRoot2 will fetch data using the merkle root of that data. This uses
// all of the async worker primitives to improve speed and throughput.
func (r *Renter) DownloadByRoot2(root crypto.Hash, offset, length uint64, timeout time.Duration) ([]byte, error) {
	// Block until there is memory available, and then ensure the memory gets
	// returned.
	if !r.memoryManager.Request(length, true) {
		return nil, errors.New("renter shut down before memory could be allocated for the project")
	}
	defer r.memoryManager.Return(length)
	data, err := r.managedDownloadByRoot(root, offset, length, timeout)
	if errors.Contains(err, ErrProjectTimedOut) {
		err = errors.AddContext(err, fmt.Sprintf("timed out after %vs", timeout.Seconds()))
	}
	return data, err
}
