package renter

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"go.sia.tech/siad/build"
	"golang.org/x/net/context"

	"gitlab.com/NebulousLabs/errors"
)

// jobTest is a minimum viable implementation for a worker job. It most
// importantly needs a channel that it can send the result of its work down, so
// the caller can see how the job panned out. Technically this is not actually
// necessary, but most jobs will need to communicate some result to the caller.
//
// There are also some variables for tracking whether the job has been executed
// or discarded, these are for testing purposes and not actually part of a
// minimum viable job.
type jobTest struct {
	// jobGeneric implements a lot of the boilerplate job code for us.
	*jobGeneric

	// When a job completes it will send a result down the resultChan.
	resultChan chan *jobTestResult

	// These are variables for tracking the execution status of the job, they
	// are only used for testing. 'staticShouldFail' tells the execution function
	// whether the job should simulate a success or a failure.
	staticShouldFail bool
	discarded        bool
	executed         bool
	mu               sync.Mutex
}

// jobTestResult is a minimum viable implementation for a worker job result.
type jobTestResult struct {
	// Generally a caller minimally needs to know if there was an error. Often
	// the caller will also be expecting some result such as a piece of data.
	staticErr error
}

// jobTestMetadata is a test struct that represents test job metadata.
type jobTestMetadata struct {
	staticField bool
}

// sendResult will send the result of a job down the resultChan. Note that
// sending the result should be done in a goroutine so that the worker does not
// get blocked if nobody is listening on the resultChan. Note that also the
// resultChan should generally be created as a buffered channel with enough
// result slots that this should never block, but defensive programming suggests
// that we should implement precautions on both ends.
func (j *jobTest) sendResult(result *jobTestResult) {
	w := j.staticQueue.staticWorker()
	err := w.renter.tg.Launch(func() {
		select {
		case j.resultChan <- result:
		case <-w.renter.tg.StopChan():
		case <-j.staticCtx.Done():
		}
	})
	if err != nil {
		panic(err)
	}
}

// callDiscard expires the job. This typically requires telling the caller that
// the job has failed.
func (j *jobTest) callDiscard(err error) {
	// Send a failed result to the caller.
	result := &jobTestResult{
		staticErr: errors.AddContext(err, "test job is being discarded"),
	}
	j.sendResult(result)

	// Mark 'j.discarded' as true so that we can verify in the test that this
	// function is being called. Do a sanity check that the job is only being
	// discarded once.
	j.mu.Lock()
	if j.discarded {
		build.Critical("double discard on job")
	}
	j.discarded = true
	j.mu.Unlock()
}

// callExecute will mark the job as executed.
func (j *jobTest) callExecute() {
	j.mu.Lock()
	j.executed = true
	staticShouldFail := j.staticShouldFail
	j.mu.Unlock()

	// Need to report a success if the job succeeded, and a fail otherwise.
	var err error
	if staticShouldFail {
		err = errors.New("job is simulated to have failed")
		j.staticQueue.callReportFailure(err)
	} else {
		j.staticQueue.callReportSuccess()
	}

	// Send the error the caller.
	result := &jobTestResult{
		staticErr: err,
	}
	j.sendResult(result)
}

// callExpectedBandwidth returns the amount of bandwidth this job is expected to
// consume.
func (j *jobTest) callExpectedBandwidth() (ul, dl uint64) {
	return 0, 0
}

// TestWorkerJobGeneric tests that all of the code for the generic worker job is
// functioning correctly.
func TestWorkerJobGeneric(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create a job queue.
	w := new(worker)
	w.renter = new(Renter)
	jq := newJobGenericQueue(w)
	cancelCtx, cancel := context.WithCancel(context.Background())

	// Create a job, add the job to the queue, and then ensure that the
	// cancelation is working correctly.
	resultChan := make(chan *jobTestResult, 1)
	j := &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,
	}
	if j.staticCanceled() {
		t.Error("job should not be canceled yet")
	}
	if !jq.callAdd(j) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	cancel()
	job := jq.callNext()
	if job != nil {
		t.Error("queue should not be returning canceled jobs")
	}
	if !j.staticCanceled() {
		t.Error("job should be reporting itself as canceled")
	}
	j.mu.Lock()
	discarded := j.discarded
	executed := j.executed
	j.mu.Unlock()
	if !discarded || executed {
		t.Error("job should not have executed but discarded")
	}
	// NOTE: the job is not expected to send a result when it has been
	// explicitly canceled. Check that no result was sent.
	select {
	case <-resultChan:
		t.Error("there should not be any result after a job was canceled successfully")
	default:
	}
	// NOTE: a job being canceled is not considered to be an error, the queue
	// will not go on cooldown. Next job should be able to succeed without any
	// sort of waiting for a cooldown.

	// Create two new jobs, add them to the queue, and then simulate the work
	// loop executing the jobs.
	cancelCtx, cancel = context.WithCancel(context.Background())
	resultChan = make(chan *jobTestResult, 1)
	j = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,
	}
	if !jq.callAdd(j) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	// Add a second job to the queue to check that the queue function is working
	// correctly.
	cancelCtx2, _ := context.WithCancel(context.Background())
	resultChan2 := make(chan *jobTestResult, 1)
	j2 := &jobTest{
		jobGeneric: newJobGeneric(cancelCtx2, jq, nil),

		resultChan: resultChan2,
	}
	if !jq.callAdd(j2) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	job = jq.callNext()
	if job == nil {
		t.Fatal("call to grab the next job failed, there should be a job ready in the queue")
	}
	// Simulate a successful execution by the control loop.
	job.callExecute()
	// There should be one more job in the queue.
	job = jq.callNext()
	if job == nil {
		t.Fatal("call to grab the next job failed, there should be a job ready in the queue")
	}
	job.callExecute()
	// Queue should be empty now.
	job = jq.callNext()
	if job != nil {
		t.Fatal("job queue should be empty")
	}
	// jobs should be marked as executed, and should not be marked as discarded.
	j.mu.Lock()
	if !j.executed || j.discarded {
		t.Error("job state indicates that the wrong code ran")
	}
	j.mu.Unlock()
	j2.mu.Lock()
	if !j2.executed || j2.discarded {
		t.Error("job state indicates that the wrong code ran")
	}
	j2.mu.Unlock()
	// There should be a result with no error in the result chan.
	select {
	case res := <-resultChan:
		if res == nil || res.staticErr != nil {
			t.Error("there should be a result with a nil error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	select {
	case res := <-resultChan2:
		if res == nil || res.staticErr != nil {
			t.Error("there should be a result with a nil error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}

	// Create several jobs and add them to the queue. Have the first job fail,
	// this should result in the worker going on cooldown and cause all of the
	// rest of the jobs to fail as well.
	cancelCtx, cancel = context.WithCancel(context.Background())
	resultChan = make(chan *jobTestResult, 1)
	j = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,

		// Set staticShouldFail to true, so the execution knows to fail the job.
		staticShouldFail: true,
	}
	if !jq.callAdd(j) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	cancelCtx2, _ = context.WithCancel(context.Background())
	resultChan2 = make(chan *jobTestResult, 1)
	j2 = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx2, jq, nil),

		resultChan: resultChan2,
	}
	if !jq.callAdd(j2) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	cancelCtx3, _ := context.WithCancel(context.Background())
	resultChan3 := make(chan *jobTestResult, 1)
	j3 := &jobTest{
		jobGeneric: newJobGeneric(cancelCtx3, jq, nil),

		resultChan: resultChan3,
	}
	if !jq.callAdd(j3) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	// Simulate execution of the first job, this should fail.
	job = jq.callNext()
	if job == nil {
		t.Fatal("there should be a job in the queue")
	}
	job.callExecute()
	// Queue should be empty now and the other jobs should be discarded.
	job = jq.callNext()
	if job != nil {
		t.Error("there should be no more jobs in the queue")
	}
	// j should be marked as executed, the others should be marked as discarded.
	j.mu.Lock()
	if !j.executed || j.discarded {
		t.Error("j indicates wrong execution path")
	}
	j.mu.Unlock()
	j2.mu.Lock()
	if j2.executed || !j2.discarded {
		t.Error("j2 indicates wrong execution path")
	}
	j2.mu.Unlock()
	j3.mu.Lock()
	if j3.executed || !j3.discarded {
		t.Error("j3 indicates wrong execution path")
	}
	j3.mu.Unlock()
	// All three jobs should be giving out errors on their resultChans.
	select {
	case res := <-resultChan:
		if res == nil || res.staticErr == nil {
			t.Error("there should be a result with an error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	select {
	case res := <-resultChan2:
		if res == nil || res.staticErr == nil {
			t.Error("there should be a result with an error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	select {
	case res := <-resultChan3:
		if res == nil || res.staticErr == nil {
			t.Error("there should be a result with an error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	// Check the recentErr and consecutive failures field of the generic job,
	// they should be set since there was a failure.
	jq.mu.Lock()
	if jq.recentErr == nil {
		t.Error("the recentErr field should be set since there was a failure")
	}
	if jq.consecutiveFailures != 1 {
		t.Error("job queue should be reporting consecutive failures")
	}
	cu := jq.cooldownUntil
	jq.mu.Unlock()

	// Check the queue is on a cooldown
	if !jq.callOnCooldown() {
		t.Error("queue should be on cooldown")
	}

	// The queue should be on cooldown now, adding a new job should fail.
	cancelCtx, cancel = context.WithCancel(context.Background())
	resultChan = make(chan *jobTestResult, 1)
	j = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,

		// Set staticShouldFail to true, so the execution knows to fail the job.
		staticShouldFail: true,
	}
	if jq.callAdd(j) {
		t.Fatal("job queue should be on cooldown")
	}
	// Sleep until the cooldown has ended.
	time.Sleep(time.Until(cu))
	// Try adding the job again, this time adding the job should succeed.
	if !jq.callAdd(j) {
		t.Fatal("job queue should be off cooldown now")
	}
	// Execute the job, which should cause a failure and more cooldown.
	job = jq.callNext()
	if job == nil {
		t.Fatal("there should be a job")
	}
	job.callExecute()
	// Drain the result of the job, make sure it's an error.
	select {
	case res := <-resultChan:
		if res == nil || res.staticErr == nil {
			t.Error("there should be a result with an error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	// Check the job execution status.
	j.mu.Lock()
	if !j.executed || j.discarded {
		t.Error("j has wrong execution flags")
	}
	j.mu.Unlock()
	// Check the queue cooldown status.
	jq.mu.Lock()
	if jq.recentErr == nil {
		t.Error("the recentErr field should be set since there was a failure")
	}
	if jq.consecutiveFailures != 2 {
		t.Error("job queue should be reporting consecutive failures")
	}
	cu = jq.cooldownUntil
	jq.mu.Unlock()
	// Sleep off the cooldown.
	time.Sleep(time.Until(cu))

	// Check the cooldown status
	if jq.callOnCooldown() {
		t.Error("queue should not be on cooldown")
	}

	// Add a job with metadata to the queue
	j5 := &jobTest{
		jobGeneric: newJobGeneric(context.Background(), jq, jobTestMetadata{
			staticField: true,
		}),
		resultChan: make(chan *jobTestResult, 1),
	}
	if !jq.callAdd(j5) {
		t.Fatal("call to add job to new job queue should succeed")
	}
	job = jq.callNext()
	if job == nil {
		t.Fatal("call to grab the next job failed, there should be a job ready in the queue")
	}
	meta, ok := job.staticGetMetadata().(jobTestMetadata)
	if !ok {
		t.Fatal("expected job metadata to be present on the job", ok, job.staticGetMetadata())
	}
	if !reflect.DeepEqual(meta, jobTestMetadata{
		staticField: true,
	}) {
		t.Fatal("unexpected metadata")
	}

	// Add one more job, and check that killing the queue kills the job.
	cancelCtx, cancel = context.WithCancel(context.Background())
	resultChan = make(chan *jobTestResult, 1)
	j = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,
	}
	if !jq.callAdd(j) {
		t.Fatal("job queue should be off cooldown now")
	}

	// Kill the queue.
	jq.callKill()
	job = jq.callNext()
	if job != nil {
		t.Fatal("after killing the queue, there should be no more jobs")
	}
	// Check that the job result is an error.
	select {
	case res := <-resultChan:
		if res == nil || res.staticErr == nil {
			t.Error("there should be a result with an error")
		}
	case <-time.After(time.Second * 3):
		t.Error("there should be a result")
	}
	// Check the job execution status.
	j.mu.Lock()
	if j.executed || !j.discarded {
		t.Error("j has wrong execution flags")
	}
	j.mu.Unlock()

	// Try adding a new job, this should fail because the queue was killed.
	cancelCtx, cancel = context.WithCancel(context.Background())
	resultChan = make(chan *jobTestResult, 1)
	j = &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),

		resultChan: resultChan,
	}
	if jq.callAdd(j) {
		t.Fatal("should not be able to add jobs after the queue has been killed")
	}
}

// TestQueueMemoryLeak makes sure that adding jobs to a queue in a tight loop
// won't cause too many allocated objects in memory.
func TestQueueMemoryLeak(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create queue.
	w := new(worker)
	w.renter = new(Renter)
	jq := newJobGenericQueue(w)

	// Prepare a job.
	cancelCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultChan := make(chan *jobTestResult, 1)
	j := &jobTest{
		jobGeneric: newJobGeneric(cancelCtx, jq, nil),
		resultChan: resultChan,
	}

	// Add the job 1 million times and remove it again.
	n := 1000000
	for i := 0; i < n; i++ {
		if !jq.callAdd(j) {
			t.Fatal("failed to add job")
		}
		jq.callNext()
	}

	// Get the memory stats and print them.
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Log("before gc", ms.HeapObjects, ms.HeapAlloc)

	// Less than 100k objects should be allocated.
	// NOTE: This number was chosen after manually testing and printing the
	// stats. During testing it turned out that running the loop above 1 million
	// times would cause the number of objects to be at around 65k vs 200+k with
	// the old code.
	if ms.HeapObjects > 100000 {
		t.Fatal("Too many allocated objects", ms.HeapObjects)
	}

	// Free memory.
	debug.FreeOSMemory()

	// Print the stats again.
	runtime.ReadMemStats(&ms)
	t.Log("after gc", ms.HeapObjects, ms.HeapAlloc)
}
