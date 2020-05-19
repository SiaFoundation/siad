package renter

import (
	"sync/atomic"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
)

type (
	// workerLoopState tracks the state of the worker loop.
	workerLoopState struct {
		// Variable to ensure only one serial job is running at a time.
		atomicSerialJobRunning uint64

		// Variables to track the total amount of async data outstanding. This
		// indicates the total amount of data that we expect to use from async
		// jobs that we have submitted for the worker.
		atomicReadDataOutstanding  uint64
		atomicWriteDataOutstanding uint64

		// The read data limit and the write data limit define how much work is
		// allowed to be outstanding before new jobs will be blocked from being
		// launched async.
		atomicReadDataLimit  uint64
		atomicWriteDataLimit uint64
	}

	// getAsyncJob defines a function which returns an async job plus a read
	// size and a write size for that job. The read and write size refer to the
	// amount of read and write network bandwidth that will be consumed by
	// calling fn(). If there is no job to perform, 'job' is expected to be nil.
	getAsyncJob func() (job func(), readSize uint64, writeSize uint64)
)

// staticSerialJobRunning indicates whether a serial job is currently running
// for the worker.
func (wls *workerLoopState) staticSerialJobRunning() bool {
	return atomic.LoadUint64(&wls.atomicSerialJobRunning) == 1
}

// staticFinishSerialJob will update the worker loop state to indicate that a
// serial job has completed.
func (wls *workerLoopState) staticFinishSerialJob() {
	atomic.StoreUint64(&wls.atomicSerialJobRunning, 0)
}

// externLaunchSerialJob will launch a serial job for the worker, ensuring that
// exclusivity is handled correctly.
//
// The 'extern' indicates that this function is only allowed to be called from
// 'threadedWorkLoop', and it is expected that only one instance of
// 'threadedWorkLoop' is ever created per-worker.
func (w *worker) externLaunchSerialJob(job func()) {
	// Mark that there is now a job running.
	atomic.StoreUint64(&w.staticLoopState.atomicSerialJobRunning, 1)
	fn := func() {
		// Execute the job in a goroutine.
		job()
		// After the job has executed, update to indicate that no serial job
		// is running.
		atomic.StoreUint64(&w.staticLoopState.atomicSerialJobRunning, 0)
		// After updating to indicate that no serial job is running, wake the
		// worker to check for a new serial job.
		w.staticWake()
	}
	err := w.renter.tg.Launch(fn)
	if err != nil {
		// Renter has closed, job will not be executed.
		atomic.StoreUint64(&w.staticLoopState.atomicSerialJobRunning, 0)
		return
	}
}

// externTryLaunchSerialJob will attempt to launch a serial job on the worker.
// Only one serial job is allowed to be running at a time (each serial job
// requires exclusive access to the worker's contract). If there is already a
// serial job running, nothing will happen.
//
// The 'extern' indicates that this function is only allowed to be called from
// 'threadedWorkLoop', and it is expected that only one instance of
// 'threadedWorkLoop' is ever created per-worker.
func (w *worker) externTryLaunchSerialJob() {
	// If there is already a serial job running, that job has exclusivity, do
	// nothing.
	if w.staticLoopState.staticSerialJobRunning() {
		return
	}

	// Check every potential serial job that the worker may be required to
	// perform. This scheduling allows a flood of jobs earlier in the list to
	// starve out jobs later in the list. At some point we will probably
	// revisit this to try and address the starvation issue.
	if w.staticNeedsPriceTableUpdate() {
		w.externLaunchSerialJob(w.staticUpdatePriceTable)
		return
	}
	if w.managedAccountNeedsRefill() {
		w.externLaunchSerialJob(w.managedRefillAccount)
		return
	}
	if w.staticFetchBackupsJobQueue.managedHasJob() {
		w.externLaunchSerialJob(w.managedPerformFetchBackupsJob)
		return
	}
	if w.staticJobQueueDownloadByRoot.managedHasJob() {
		w.externLaunchSerialJob(w.managedLaunchJobDownloadByRoot)
		return
	}
	if w.managedHasDownloadJob() {
		w.externLaunchSerialJob(w.managedPerformDownloadChunkJob)
		return
	}
	if w.managedHasUploadJob() {
		w.externLaunchSerialJob(w.managedPerformUploadChunkJob)
		return
	}
}

// externLaunchAsyncJob accepts a function to retrieve a job and then uses that
// to retrieve a job and launch it. The bandwidth consumption will be updated as
// the job starts and finishes.
func (w *worker) externLaunchAsyncJob(getJob getAsyncJob) bool {
	// Get the job and its resource requirements.
	job, uploadBandwidth, downloadBandwidth := getJob()
	if job == nil {
		// No job available.
		return false
	}

	// Add the resource requirements to the worker loop state.
	atomic.AddUint64(&w.staticLoopState.atomicReadDataOutstanding, downloadBandwidth)
	atomic.AddUint64(&w.staticLoopState.atomicWriteDataOutstanding, uploadBandwidth)
	fn := func() {
		job()
		// Subtract the outstanding data now that the job is complete. Atomic
		// subtraction works by adding and using some bit tricks.
		atomic.AddUint64(&w.staticLoopState.atomicReadDataOutstanding, ^uint64(downloadBandwidth-1))
		atomic.AddUint64(&w.staticLoopState.atomicWriteDataOutstanding, ^uint64(uploadBandwidth-1))
		// Wake the worker to run any additional async jobs that may have been
		// blocked / ignored because there was not enough bandwidth available.
		w.staticWake()
	}
	err := w.renter.tg.Launch(fn)
	if err != nil {
		// Renter has closed, but we want to represent that the work was
		// processed anyway - returning true indicates that the worker should
		// continue processing jobs.
		atomic.AddUint64(&w.staticLoopState.atomicReadDataOutstanding, ^uint64(downloadBandwidth-1))
		atomic.AddUint64(&w.staticLoopState.atomicWriteDataOutstanding, ^uint64(uploadBandwidth-1))
		return true
	}
	return true
}

// externTryLaunchAsyncJob will look at the async jobs which are in the worker
// queue and attempt to launch any that are ready. The job launcher will fail if
// the price table is out of date or if the worker account is empty.
//
// The job launcher will also fail if the worker has too much work in jobs
// already queued. Every time a job is launched, a bandwidth estimate is made.
// The worker will not allow more than a certain amount of bandwidth to be
// queued at once to prevent jobs from being spread too thin and sharing too
// much bandwidth.
func (w *worker) externTryLaunchAsyncJob() bool {
	// Hosts that do not support the async protocol cannot do async jobs.
	cache := w.staticCache()
	if build.VersionCmp(cache.staticHostVersion, minAsyncVersion) < 0 {
		w.managedDiscardAsyncJobs()
		return false
	}

	// A valid price table is required to perform async tasks.
	if !w.staticPriceTable().staticValid() {
		w.managedDiscardAsyncJobs()
		return false
	}

	// If the account is on cooldown, drop all async jobs.
	if w.staticAccount.managedOnCooldown() {
		w.managedDiscardAsyncJobs()
		return false
	}

	// Verify that the worker has not reached its limits for doing multiple
	// jobs at once.
	readLimit := atomic.LoadUint64(&w.staticLoopState.atomicReadDataLimit)
	writeLimit := atomic.LoadUint64(&w.staticLoopState.atomicWriteDataLimit)
	readOutstanding := atomic.LoadUint64(&w.staticLoopState.atomicReadDataOutstanding)
	writeOutstanding := atomic.LoadUint64(&w.staticLoopState.atomicWriteDataOutstanding)
	if readOutstanding > readLimit || writeOutstanding > writeLimit {
		// Worker does not need to dump jobs, it is making progress, it's just
		// not launching any new jobs until its current jobs finish up.
		return false
	}

	// Check every potential async job that can be launched.
	if w.externLaunchAsyncJob(w.staticJobHasSectorQueue.callNext) {
		return true
	}
	if w.externLaunchAsyncJob(w.staticJobReadSectorQueue.callNext) {
		return true
	}
	return false
}

// managedBlockUntilReady will block until the worker has internet connectivity.
// 'false' will be returned if a kill signal is received or if the renter is
// shut down before internet connectivity is restored. 'true' will be returned
// if internet connectivity is successfully restored.
func (w *worker) managedBlockUntilReady() bool {
	// Check if the worker has received a kill signal, or if the renter has
	// received a stop signal.
	select {
	case <-w.renter.tg.StopChan():
		return false
	case <-w.killChan:
		return false
	default:
	}

	// Check internet connectivity. If the worker does not have internet
	// connectivity, block until connectivity is restored.
	for !w.renter.g.Online() {
		select {
		case <-w.renter.tg.StopChan():
			return false
		case <-w.killChan:
			return false
		case <-time.After(offlineCheckFrequency):
		}
	}
	return true
}

// managedDiscardAsyncJobs will drop all of the worker's async jobs because the
// worker has not met sufficient conditions to retain async jobs.
func (w *worker) managedDiscardAsyncJobs() {
	w.managedDiscardJobsHasSector()
	w.managedDiscardJobsReadSector()
}

// threadedWorkLoop is a perpetual loop run by the worker that accepts new jobs
// and performs them. Work is divided into two types of work, serial work and
// async work. Serial work requires exclusive access to the worker's contract,
// meaning that only one of these tasks can be performed at a time.  Async work
// can be performed with high parallelism.
func (w *worker) threadedWorkLoop() {
	// Upon shutdown, release all jobs.
	defer w.managedKillUploading()
	defer w.managedKillDownloading()
	defer w.managedKillFetchBackupsJobs()
	defer w.managedKillJobsDownloadByRoot()
	defer w.managedKillJobsHasSector()
	defer w.managedKillJobsReadSector()
	defer w.managedKillJobsDownloadByRoot()

	if build.VersionCmp(w.staticCache().staticHostVersion, minAsyncVersion) >= 0 {
		// The worker cannot execute any async tasks unles the price table of the
		// host is known, the balance of the worker account is known, and the
		// account has sufficient funds in it. This update is done as a blocking
		// update to ensure nothing else runs until the price table is available.
		w.staticUpdatePriceTable()
		// TODO: Do a balance query on the host right here. Even if we had a clean
		// shutdown and know the exact balance, we should still be asking the host
		// what our balance is, because we don't want the host to be able to
		// distinguish between the times that we know our balance and the times that
		// we don't. Checking right at startup also allows us to give a quick
		// honesty check on the host.
		//
		// This update is done as a blocking update to ensure nothing else runs
		// until the account has filled.
		w.managedRefillAccount()
	}

	// The worker will continuously perform jobs in a loop.
	for {
		// There are certain conditions under which the worker should either
		// block or exit. This function will block until those conditions are
		// met, returning 'true' when the worker can proceed and 'false' if the
		// worker should exit.
		if !w.managedBlockUntilReady() {
			return
		}

		// Update the cache for the worker if needed.
		if !w.staticTryUpdateCache() {
			w.renter.log.Printf("worker %v is being killed because the cache could not be updated", w.staticHostPubKeyStr)
			return
		}

		// Attempt to launch a serial job. If there is already a job running,
		// this will no-op. If no job is running, a goroutine will be spun up
		// to run a job, this call is non-blocking.
		w.externTryLaunchSerialJob()

		// Attempt to launch an async job. If the async job launches
		// successfully, skip the blocking phase and attempt to launch another
		// async job.
		//
		// The worker will only allow a handful of async jobs to be running at
		// once, to protect the total usage of the network connection. The
		// worker wants to avoid a situation where 1,000 jobs each requiring a
		// large amount of bandwidth are all running simultaneously. If the
		// jobs are tiny in terms of resource footprints, the worker will allow
		// more of them to be running at once.
		if w.externTryLaunchAsyncJob() {
			continue
		}

		// Block until:
		//    + New work has been submitted
		//    + The worker is killed
		//    + The renter is stopped
		select {
		case <-w.wakeChan:
			continue
		case <-w.killChan:
			return
		case <-w.renter.tg.StopChan():
			return
		}
	}
}
