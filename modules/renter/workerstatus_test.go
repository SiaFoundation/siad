package renter

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// TestWorkerAccountStatus is a small unit test that verifies the output of the
// `managedStatus` method on the worker's account.
func TestWorkerAccountStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableCriticalOnMaxBalance{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// allow the worker some time to fetch a PT and fund its EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// fetch the worker's account status and verify its output
	a := w.staticAccount
	status := a.managedStatus()
	if !(!status.AvailableBalance.IsZero() &&
		status.AvailableBalance.Equals(w.staticBalanceTarget) &&
		status.RecentErr == "" &&
		status.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected account status", ToJSON(status))
	}

	// ensure the worker is not on maintenance cooldown
	if w.managedOnMaintenanceCooldown() {
		t.Fatal("Unexpected maintenance cooldown")
	}

	// nullify the account balance to ensure refilling triggers a max balance
	// exceeded on the host causing the worker's account to cool down
	a.mu.Lock()
	a.balance = types.ZeroCurrency
	a.mu.Unlock()
	w.managedRefillAccount()

	// fetch the worker's account status and verify the error is being set
	status = a.managedStatus()
	if !(status.AvailableBalance.IsZero() &&
		status.AvailableBalance.IsZero() &&
		status.RecentErr != "" &&
		status.RecentErrTime != time.Time{}) {
		t.Fatal("Unexpected account status", ToJSON(status))
	}

	// ensure the worker's RHP3 system is on cooldown
	if !w.managedOnMaintenanceCooldown() {
		t.Fatal("Expected RHP3 to have been put on cooldown")
	}
}

// TestWorkerPriceTableStatus is a small unit test that verifies the output of
// the worker's `staticPriceTableStatus` method.
func TestWorkerPriceTableStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	var hostClosed bool
	defer func() {
		if hostClosed {
			if err := wt.rt.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	w := wt.worker

	// allow the worker some time to fetch a PT and fund its EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// fetch the worker's pricetable status and verify its output
	status := w.staticPriceTableStatus()
	if !(status.Active == true &&
		status.RecentErr == "" &&
		status.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected price table status", ToJSON(status))
	}

	// close the host to ensure the update PT call fails
	err = wt.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	hostClosed = true

	// trigger an update - to avoid sleeping until `UpdateTime` we manually
	// overwrite it on the PT and call wake
	wpt := w.staticPriceTable()
	wpt.staticUpdateTime = time.Now()
	w.staticSetPriceTable(wpt)
	w.staticWake()

	// fetch the worker's pricetable status
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		status = w.staticPriceTableStatus()
		if !(status.Active == true &&
			status.RecentErr != "" &&
			status.RecentErrTime != time.Time{}) {
			return fmt.Errorf("Unexpected pricetable status %v", ToJSON(status))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerReadJobStatus is a small unit test that verifies the output of the
// `callReadJobStatus` method on the worker.
func TestWorkerReadJobStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	w := wt.worker

	var hostClosed bool
	defer func() {
		if hostClosed {
			if err := wt.rt.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// allow the worker some time to fetch a PT and fund its EA
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// fetch the worker's read jobs status and verify its output
	status := w.callReadJobStatus()
	if !(status.ConsecutiveFailures == 0 &&
		status.JobQueueSize == 0 &&
		status.RecentErr == "" &&
		status.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected read job status", ToJSON(status))
	}

	// close the host to ensure the job fails
	err = wt.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	hostClosed = true

	// add the job to the worker
	ctx := context.Background()
	rc := make(chan *jobReadResponse)

	jhs := &jobReadSector{
		jobRead: jobRead{
			staticLength:       modules.SectorSize,
			staticResponseChan: rc,

			jobGeneric: &jobGeneric{
				staticCtx:   ctx,
				staticQueue: w.staticJobReadQueue,
				staticMetadata: jobReadMetadata{
					staticSpendingCategory: categoryDownload,
					staticWorker:           w,
				},
			},
		},
		staticOffset: 0,
	}
	if !w.staticJobReadQueue.callAdd(jhs) {
		t.Fatal("Could not add job to queue")
	}

	// verify the status in a build.Retry to allow the worker some time to
	// process the job
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		status = w.callReadJobStatus()
		if !(status.ConsecutiveFailures == 1 &&
			status.RecentErr != "" &&
			status.RecentErrTime != time.Time{}) {
			return fmt.Errorf("Unexpected read job status %v", ToJSON(status))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestWorkerHasSectorJobStatus is a small unit test that verifies the output of
// the `callHasSectorJobStatus` method on the worker.
func TestWorkerHasSectorJobStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a new worker tester
	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var hostClosed bool
	defer func() {
		if hostClosed {
			if err := wt.rt.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// allow the worker some time to fund its EA
	if err := build.Retry(600, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// fetch the worker's (initial) HS jobs status and verify its output
	status := w.callHasSectorJobStatus()
	if !(status.ConsecutiveFailures == 0 &&
		status.JobQueueSize == 0 &&
		status.RecentErr == "" &&
		status.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected has sector job status", ToJSON(status))
	}

	// prevent the worker from doing any work
	current := atomic.LoadUint64(&w.staticLoopState.atomicReadDataOutstanding)
	limit := atomic.LoadUint64(&w.staticLoopState.atomicReadDataLimit)
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataOutstanding, limit+1)

	hsRespChan := make(chan *jobHasSectorResponse, 10)

	// add a job to the worker
	jhs := w.newJobHasSector(context.Background(), hsRespChan, crypto.Hash{})
	if !w.staticJobHasSectorQueue.callAdd(jhs) {
		t.Fatal("Could not add job to queue")
	}

	// fetch the worker's has sector job status again and verify its output
	status = w.callHasSectorJobStatus()
	if status.JobQueueSize == 0 {
		t.Fatal("Unexpected has sector job status", ToJSON(status))
	}

	// restore the read limit
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataOutstanding, current)

	// verify the status in a build.Retry to allow the worker some time to
	// process the jobs
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		status = w.callHasSectorJobStatus()
		if status.AvgJobTime == 0 ||
			status.JobQueueSize != 0 {
			return fmt.Errorf("Unexpected has sector job status %v", ToJSON(status))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// prevent the worker from doing any work
	current = atomic.LoadUint64(&w.staticLoopState.atomicReadDataOutstanding)
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataOutstanding, limit+1)

	// add another job to the worker
	jhs = w.newJobHasSector(context.Background(), hsRespChan, crypto.Hash{})
	if !w.staticJobHasSectorQueue.callAdd(jhs) {
		t.Fatal("Could not add job to queue")
	}

	// close the host to ensure the job will fail
	err = wt.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	hostClosed = true

	// restore the read limit
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataOutstanding, current)

	// verify the error status in a build.Retry
	if err := build.Retry(600, 100*time.Millisecond, func() error {
		status = w.callHasSectorJobStatus()
		if !(status.ConsecutiveFailures == 1 &&
			status.RecentErr != "" &&
			status.RecentErrTime != time.Time{}) {
			return fmt.Errorf("Unexpected has sector job status %v", ToJSON(status))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// ToJSON is a helper function that wraps the jsonMarshalIndent function
func ToJSON(a interface{}) string {
	json, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		panic(err)
	}
	return string(json)
}

// TestWorkerRegistryJobStatus tests the ReadRegistry and UpdateRegistry related
// stats.
func TestWorkerRegistryJobStatus(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var hostClosed bool
	defer func() {
		if hostClosed {
			if err := wt.rt.Close(); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// allow the worker some time to fetch a PT and fund its EA
	if err := build.Retry(600, 100*time.Millisecond, func() error {
		if w.staticAccount.managedMinExpectedBalance().IsZero() {
			return errors.New("account not funded yet")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// fetch the worker's has sector jobs status and verify its output
	statusRead := w.callReadRegistryJobsStatus()
	if !(statusRead.ConsecutiveFailures == 0 &&
		statusRead.JobQueueSize == 0 &&
		statusRead.OnCooldown == false &&
		statusRead.OnCooldownUntil == time.Time{} &&
		statusRead.RecentErr == "" &&
		statusRead.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected job status", ToJSON(statusRead))
	}
	statusUpdate := w.callUpdateRegistryJobsStatus()
	if !(statusUpdate.ConsecutiveFailures == 0 &&
		statusUpdate.JobQueueSize == 0 &&
		statusUpdate.OnCooldown == false &&
		statusUpdate.OnCooldownUntil == time.Time{} &&
		statusUpdate.RecentErr == "" &&
		statusUpdate.RecentErrTime == time.Time{}) {
		t.Fatal("Unexpected job status", ToJSON(statusUpdate))
	}

	// prevent the worker from doing any work by manipulating its read limit
	current := atomic.LoadUint64(&w.staticLoopState.atomicReadDataOutstanding)
	limit := atomic.LoadUint64(&w.staticLoopState.atomicReadDataLimit)
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataLimit, limit)

	// add 1 job each to the worker.
	ctx := context.Background()
	rrc := make(chan *jobReadRegistryResponse)
	urc := make(chan *jobUpdateRegistryResponse)
	jrr := w.newJobReadRegistry(ctx, rrc, types.SiaPublicKey{}, crypto.Hash{})
	jur := w.newJobUpdateRegistry(ctx, urc, types.SiaPublicKey{}, modules.SignedRegistryValue{})
	if !w.staticJobReadRegistryQueue.callAdd(jrr) {
		t.Fatal("Could not add job to queue")
	}
	if !w.staticJobUpdateRegistryQueue.callAdd(jur) {
		t.Fatal("Could not add job to queue")
	}

	// fetch the worker's jobs status again.
	statusRead = w.callReadRegistryJobsStatus()
	statusUpdate = w.callUpdateRegistryJobsStatus()
	if statusRead.JobQueueSize != 1 {
		t.Fatal("Unexpected job status", ToJSON(statusRead))
	}
	if statusUpdate.JobQueueSize != 1 {
		t.Fatal("Unexpected job status", ToJSON(statusUpdate))
	}

	// close the host to ensure the jobs will fail
	err = wt.host.Close()
	if err != nil {
		t.Fatal(err)
	}
	hostClosed = true

	// restore the read limit
	atomic.StoreUint64(&w.staticLoopState.atomicReadDataLimit, current)

	// verify the status in a build.Retry to allow the worker some time to
	// process the jobs
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		statusRead = w.callReadRegistryJobsStatus()
		if !(statusRead.ConsecutiveFailures == 1 &&
			statusRead.OnCooldown == true &&
			statusRead.OnCooldownUntil != time.Time{} &&
			statusRead.RecentErr != "" &&
			statusRead.RecentErrTime != time.Time{}) {
			return fmt.Errorf("Unexpected job %v", ToJSON(statusRead))
		}
		statusUpdate = w.callUpdateRegistryJobsStatus()
		if !(statusUpdate.ConsecutiveFailures == 1 &&
			statusUpdate.OnCooldown == true &&
			statusUpdate.OnCooldownUntil != time.Time{} &&
			statusUpdate.RecentErr != "" &&
			statusUpdate.RecentErrTime != time.Time{}) {
			return fmt.Errorf("Unexpected job %v", ToJSON(statusUpdate))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
