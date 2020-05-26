package renter

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// dependencyTestJobSerialExecution is a special dependency to change the
// behavior of 'externTryLaunchSerialJob' to check that it does a good job of
// only having a single serial job run at a time.
type dependencyTestJobSerialExecution struct {
	modules.ProductionDependencies

	// Making this a time.Duration means we don't have to typecast it when
	// comparing the number of jobs to the amount of time it took to complete
	// them.
	jobsCompleted time.Duration

	staticWorker *worker
	mu           sync.Mutex
}

// Disrupt will check for two specific disrupts and respond accordingly.
func (d *dependencyTestJobSerialExecution) Disrupt(s string) bool {
	w := d.staticWorker
	if s != "TestJobSerialExecution" {
		return false
	}

	// The whole purpose of the job is to make sure that the job continues
	// to be marked as 'running' while it is running.
	//
	// There's a mutex here to ensure that the job does not complete before
	// we can check that the job has been marked as running after launching
	// the job.
	continueChan := make(chan struct{})
	w.externLaunchSerialJob(func() {
		if atomic.LoadUint64(&w.staticLoopState.atomicSerialJobRunning) != 1 {
			build.Critical("running a job without having the serial job running flag set")
		}
		time.Sleep(time.Millisecond * 100)
		if atomic.LoadUint64(&w.staticLoopState.atomicSerialJobRunning) != 1 {
			build.Critical("running a job without having the serial job running flag set")
		}

		// This is a flush, the job will not complete until the check
		// outside of this job has completed, solving a potential race
		// condition where the job completes before we check that the job is
		// still marked as running.
		<-continueChan

		// Signal that a job has completed.
		d.mu.Lock()
		d.jobsCompleted++
		d.mu.Unlock()
	})
	if atomic.LoadUint64(&w.staticLoopState.atomicSerialJobRunning) != 1 {
		build.Critical("running a job when another job is already running")
	}
	close(continueChan)
	return true
}

// TestJobSerialExecution checks that only one serial job for the worker is
// allowed to run at once.
func TestJobSerialExecution(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a stub worker.
	d := &dependencyTestJobSerialExecution{}
	w := new(worker)
	w.renter = new(Renter)
	w.renter.deps = d
	d.staticWorker = w

	// Initialize a worker cache & snapshot queue
	wc := new(workerCache)
	atomic.StorePointer(&w.atomicCache, unsafe.Pointer(wc))
	w.initJobUploadSnapshotQueue()

	// Launch a bunch of serial jobs in the worker. Each job that succeeded
	// should take about 100ms to complete, we launch jobs 25ms apart for this
	// reason. To minimize code clutter, there is no shared state between the
	// job that runs and what happens here, safety is instead checked using
	// sanity checks within the job that runs.
	start := time.Now()
	for i := 0; i < 100; i++ {
		w.externTryLaunchSerialJob()
		time.Sleep(time.Millisecond * 25)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.jobsCompleted > 30 || d.jobsCompleted < 20 {
		t.Error("job serializer seems to be running the wrong number of jobs", d.jobsCompleted)
	}
	if time.Since(start) < d.jobsCompleted*100*time.Millisecond {
		t.Error("job serializer should be ensuring that at most one job completes per 100ms")
	}
}

// dependencyTestAsyncJobLauncher is a dependency to change the behavior of
// 'externTryLaunchAsyncJob' to ensure that the launcher is functioning
// correctly.
type dependencyTestAsyncJobLauncher struct {
	modules.ProductionDependencies

	jobsRunning   time.Duration
	jobsCompleted time.Duration

	staticWorker *worker
	mu           sync.Mutex
}

// getAsyncJob will return a job that can be launched asynchronously.
func (d *dependencyTestAsyncJobLauncher) getAsyncJob() (func(), uint64, uint64) {
	return func() {
		// Check that only a limited number of jobs run at once.
		d.mu.Lock()
		d.jobsRunning++
		if d.jobsRunning > 11 {
			build.Critical("too many async jobs running at once")
		}
		d.mu.Unlock()
		time.Sleep(time.Millisecond * 100)

		// Count the total number of jobs that have completed.
		d.mu.Lock()
		d.jobsRunning--
		d.jobsCompleted++
		d.mu.Unlock()
	}, 1e6, 1e6
}

// Disrupt will check that async jobs being launched are running correctly.
func (d *dependencyTestAsyncJobLauncher) Disrupt(s string) bool {
	if s != "TestAsyncJobLaunches" {
		return false
	}
	d.staticWorker.externLaunchAsyncJob(d.getAsyncJob)
	return true
}

// TestJobAsync checks that async job launches works as intended.
func TestJobAsync(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a stub worker.
	d := &dependencyTestAsyncJobLauncher{}
	w := new(worker)
	w.renter = new(Renter)
	w.renter.deps = d
	w.staticLoopState.atomicReadDataLimit = 10e6
	w.staticLoopState.atomicWriteDataLimit = 10e6
	d.staticWorker = w

	// Launch a bunch of async jobs in the worker. We try to launch jobs 5ms
	// apart, and 10 jobs are allowed to run at once, and jobs take 100ms to
	// complete. This means we should have roughly 10 successful launches
	// followed by 10 failed launches, repeating, potentially occasionally
	// skipping a missed job.
	start := time.Now()
	for i := 0; i < 100; i++ {
		w.externTryLaunchAsyncJob()
		time.Sleep(time.Millisecond * 5)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.jobsCompleted > 65 || d.jobsCompleted < 40 {
		t.Error("async job launcher seems to be running the wrong number of jobs", d.jobsCompleted)
	}
	if time.Since(start) < d.jobsCompleted*100*time.Millisecond/10 {
		t.Error("job serializer should be ensuring that at most ten jobs complete per 100ms", time.Since(start), d.jobsCompleted)
	}
}
