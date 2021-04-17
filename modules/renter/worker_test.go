package renter

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/threadgroup"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// workerTester is a helper type which contains a renter, host and worker that
// communicates with that host.
type workerTester struct {
	rt   *renterTester
	host modules.Host
	*worker
}

// newWorkerTester creates a new worker for testing.
func newWorkerTester(name string) (*workerTester, error) {
	return newWorkerTesterCustomDependency(name, modules.ProdDependencies, modules.ProdDependencies)
}

// newWorkerTesterCustomDependency creates a new worker for testing with a
// custom depency.
func newWorkerTesterCustomDependency(name string, renterDeps modules.Dependencies, hostDeps modules.Dependencies) (*workerTester, error) {
	// Create the renter.
	rt, err := newRenterTesterWithDependency(filepath.Join(name, "renter"), renterDeps)
	if err != nil {
		return nil, err
	}

	// Set an allowance.
	err = rt.renter.hostContractor.SetAllowance(modules.DefaultAllowance)
	if err != nil {
		return nil, err
	}

	// Add a host.
	host, err := rt.addCustomHost(filepath.Join(rt.dir, "host"), hostDeps)
	if err != nil {
		return nil, err
	}

	// Wait for worker to show up.
	var w *worker
	err = build.Retry(100, 100*time.Millisecond, func() error {
		_, err := rt.miner.AddBlock()
		if err != nil {
			return err
		}
		rt.renter.staticWorkerPool.callUpdate()
		workers := rt.renter.staticWorkerPool.callWorkers()
		if len(workers) != 1 {
			return fmt.Errorf("expected %v workers but got %v", 1, len(workers))
		}
		w = workers[0]
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Schedule a price table update for a brand new one.
	w.staticSchedulePriceTableUpdate(false)

	// Wait for the price table to be updated.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		pt := w.staticPriceTable()
		if pt.staticUpdateTime.Before(time.Now()) {
			return errors.New("price table not updated")
		}
		return nil
	})

	return &workerTester{
		rt:     rt,
		host:   host,
		worker: w,
	}, nil
}

// Close closes the renter and host.
func (wt *workerTester) Close() error {
	var err1, err2 error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		err1 = wt.rt.Close()
		wg.Done()
	}()
	go func() {
		err2 = wt.host.Close()
		wg.Done()
	}()
	wg.Wait()
	return errors.Compose(err1, err2)
}

// TestNewWorkerTester creates a new worker
func TestNewWorkerTester(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestReadOffsetCorruptProof tests that ReadOffset jobs correctly verify the
// merkle proof returned by the host and reject data that doesn't match said
// proof.
func TestReadOffsetCorruptedProof(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	deps := dependencies.NewDependencyCorruptMDMOutput()
	wt, err := newWorkerTesterCustomDependency(t.Name(), modules.ProdDependencies, deps)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	backup := modules.UploadedBackup{
		Name:           "foo",
		CreationDate:   types.CurrentTimestamp(),
		Size:           10,
		UploadProgress: 0,
	}

	// Upload a snapshot to fill the first sector of the contract.
	err = wt.UploadSnapshot(context.Background(), backup, fastrand.Bytes(int(backup.Size)))
	if err != nil {
		t.Fatal(err)
	}
	// Download the first sector partially and then fully since both actions
	// require different proofs.
	_, err = wt.ReadOffset(context.Background(), categorySnapshotDownload, 0, modules.SectorSize/2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = wt.ReadOffset(context.Background(), categorySnapshotDownload, 0, modules.SectorSize)
	if err != nil {
		t.Fatal(err)
	}

	// Do it again but this time corrupt the output to make sure the proof
	// doesn't match.
	deps.Fail()
	_, err = wt.ReadOffset(context.Background(), categorySnapshotDownload, 0, modules.SectorSize/2)
	if err == nil || !strings.Contains(err.Error(), "verifying proof failed") {
		t.Fatal(err)
	}

	// Retry since the worker might be on a cooldown.
	err = build.Retry(100, 100*time.Millisecond, func() error {
		deps.Fail()
		_, err = wt.ReadOffset(context.Background(), categorySnapshotDownload, 0, modules.SectorSize)
		if err == nil || !strings.Contains(err.Error(), "verifying proof failed") {
			return fmt.Errorf("unexpected error %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestManagedAsyncReady is a unit test that probes the 'managedAsyncReady'
// function on the worker
func TestManagedAsyncReady(t *testing.T) {
	w := new(worker)
	w.initJobHasSectorQueue()
	w.initJobReadQueue()
	w.initJobLowPrioReadQueue()
	w.initJobReadRegistryQueue()
	w.initJobUpdateRegistryQueue()

	timeInFuture := time.Now().Add(time.Hour)
	timeInPast := time.Now().Add(-time.Hour)

	// ensure pt is considered valid
	w.newPriceTable()
	w.staticPriceTable().staticExpiryTime = timeInFuture

	// ensure the worker has a maintenancestate, by default it will pass
	w.newMaintenanceState()

	// verify worker is considered async ready
	if !w.managedAsyncReady() {
		t.Fatal("unexpected")
	}

	// tweak the price table to make it not ready
	badWorkerPriceTable := w
	badWorkerPriceTable.staticPriceTable().staticExpiryTime = timeInPast
	if badWorkerPriceTable.managedAsyncReady() {
		t.Fatal("unexpected")
	}

	// tweak the maintenancestate making it non ready
	badWorkerMaintenanceState := w
	badWorkerMaintenanceState.staticMaintenanceState.cooldownUntil = timeInFuture
	if badWorkerMaintenanceState.managedAsyncReady() {
		t.Fatal("unexpected")
	}
}

// TestJobQueueInitialEstimate verifies the initial time estimates are set on
// both the HS and RJ queues right after performing the pricetable update for
// the first time.
func TestJobQueueInitialEstimate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := wt.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// verify it has set the initial estimates on both queues
	if w.staticJobHasSectorQueue.callExpectedJobTime() == 0 {
		t.Fatal("unexpected")
	}
	if w.staticJobReadQueue.callExpectedJobTime(fastrand.Uint64n(1<<24)) == 0 {
		t.Fatal("unexpected")
	}
}

// TestWorkerSpending is a unit test that verifies several actions and whether
// or not those actions 's spending is properly reflected in the contract
// header.
func TestWorkerSpending(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a worker that's not running its worker loop.
	wt, err := newWorkerTesterCustomDependency(t.Name(), &dependencies.DependencyDisableWorker{}, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		// Ignore threadgroup stopped error since we are manually closing the
		// threadgroup of the worker.
		if err := wt.Close(); err != nil && !errors.Contains(err, threadgroup.ErrStopped) {
			t.Fatal(err)
		}
	}()
	w := wt.worker

	// getRenterContract is a helper function that fetches the contract
	getRenterContract := func() modules.RenterContract {
		host := w.staticHostPubKey
		rc, found := w.renter.hostContractor.ContractByPublicKey(host)
		if !found {
			t.Fatal("unexpected")
		}
		return rc
	}
	rc := getRenterContract()

	// Assert the initial spending metrics are all zero
	if !rc.FundAccountSpending.IsZero() || !rc.MaintenanceSpending.Sum().IsZero() || !rc.UploadSpending.IsZero() {
		t.Fatal("unexpected")
	}

	// Get a price table and verify whether the spending cost is reflected in
	// the spending metrics.
	wt.staticUpdatePriceTable()
	rc = getRenterContract()
	pt := wt.staticPriceTable().staticPriceTable
	if !rc.MaintenanceSpending.UpdatePriceTableCost.Equals(pt.UpdatePriceTableCost) {
		t.Fatal("unexpected")
	}

	// Manually refill the account and verify whether the spending costs are
	// reflected in the spending metrics.
	w.managedRefillAccount()
	rc = getRenterContract()
	if !rc.MaintenanceSpending.FundAccountCost.Equals(pt.FundAccountCost) || rc.FundAccountSpending.IsZero() {
		t.Fatal("unexpected")
	}

	// Manually sync the account balance and verify whether the spending costs
	// are reflected in the spending metrics.
	w.externSyncAccountBalanceToHost()
	rc = getRenterContract()
	if !rc.MaintenanceSpending.FundAccountCost.Equals(pt.AccountBalanceCost) {
		t.Fatal("unexpected")
	}

	// Verify the sum is equal to the cost of the 3 RPCs we've just performed.
	if !rc.MaintenanceSpending.Sum().Equals(pt.AccountBalanceCost.Add(pt.UpdatePriceTableCost).Add(pt.FundAccountCost)) {
		t.Fatal("unexpected")
	}

	// Upload a snapshot and verify whether the spending metrics reflect the
	// upload.
	uploadSnapshotRespChan := make(chan *jobUploadSnapshotResponse)
	jus := &jobUploadSnapshot{
		staticSiaFileData:  fastrand.Bytes(100),
		staticResponseChan: uploadSnapshotRespChan,
		jobGeneric:         newJobGeneric(context.Background(), w.staticJobUploadSnapshotQueue, modules.UploadedBackup{UID: [16]byte{3, 2, 1}}),
	}
	w.externLaunchSerialJob(jus.callExecute)
	select {
	case <-uploadSnapshotRespChan:
	case <-time.After(time.Minute):
		t.Fatal("unexpected timeout")
	}
	rc = getRenterContract()
	if rc.UploadSpending.IsZero() {
		t.Fatal("unexpected")
	}
}
