package renter

import (
	"bytes"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestWorkerMaintenanceCoolDown verifies the functionality of the worker's
// cooldown of the RHP3 related subsystems.
func TestWorkerMaintenanceCoolDown(t *testing.T) {
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

	// wait until the worker is done with its maintenance tasks - this basically
	// ensures we have a working worker, with valid PT and funded EA
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			return errors.New("worker not ready with maintenance")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// verify the worker is not on an maintenance cooldown
	if w.managedOnMaintenanceCooldown() {
		t.Fatal("Unexpected maintenance cooldown")
	}

	// set a negative balance, tricking the worker into thinking it has to
	// refill
	w.staticAccount.mu.Lock()
	w.staticAccount.negativeBalance = w.staticAccount.balance
	w.staticAccount.mu.Unlock()

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the worker has been put on maintenance cooldown
	if !w.managedOnMaintenanceCooldown() {
		t.Fatal("Expected maintenance cooldown")
	}

	// the workerloop should have synced the account balance
	if err := build.Retry(100, 100*time.Millisecond, func() error {
		w.staticAccount.mu.Lock()
		defer w.staticAccount.mu.Unlock()
		if !w.staticAccount.negativeBalance.IsZero() {
			return errors.New("worker account balance not reset")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// run a couple of has sector jobs to spend money
	cc := make(chan struct{})
	rc := make(chan *jobHasSectorResponse)
	jhs := w.newJobHasSector(cc, rc, crypto.Hash{})
	for i := 0; i < 100; i++ {
		if !w.staticJobHasSectorQueue.callAdd(jhs) {
			t.Fatal("could not add job to queue")
		}
	}

	// manually trigger a refill
	w.managedRefillAccount()

	// verify the account is not on cooldown
	if w.managedOnMaintenanceCooldown() {
		t.Fatal("Worker's RHP3 subsystems should not be on cooldown")
	}
}

// TestRHP2DownloadOnMaintenanceCoolDown verifies the worker correctly processes
// download jobs. Note that this are RHP2 downloads, this test will verify in
// particular that these jobs manage to succeed even if the worker is on
// (RHP3) maintenance cool down.
func TestRHP2DownloadOnMaintenanceCoolDown(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create a new worker tester
	deps := dependencies.NewDependencyInterruptNewStreamTimeout()

	wt, err := newWorkerTesterCustomDependency(t.Name(), deps, &modules.ProductionDependencies{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// ensure the worker is on maintenance cooldown, this should be the case as
	// we've initialised the renter with a dependency that times out acuiring a
	// stream to the host
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if !wt.worker.managedOnMaintenanceCooldown() {
			return errors.New("Worker not on maintenance cool down")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// prepare upload params
	sp, err := modules.NewSiaPath(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	fup, err := fileUploadParamsFromLUP(modules.SkyfileUploadParameters{
		SiaPath:             sp,
		DryRun:              false,
		Force:               false,
		BaseChunkRedundancy: 2,
	})
	fup.CipherType = crypto.TypePlain // don't care about encryption
	if err != nil {
		t.Fatal(err)
	}

	// upload some data
	data := fastrand.Bytes(int(modules.SectorSize))
	reader := bytes.NewReader(data)
	err = wt.renter.UploadStreamFromReader(fup, reader)
	if err != nil {
		t.Fatal(err)
	}

	// download the data using the legacy (!) download, which is RHP2
	root := crypto.MerkleRoot(data)
	downloaded, err := wt.renter.DownloadByRootLegacy(root, 0, modules.SectorSize, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, data) {
		t.Fatal("Downloaded data did not match uploaded data")
	}

	// grab the cooldown until
	wms := wt.worker.staticMaintenanceState
	wms.mu.Lock()
	cdu := wms.cooldownUntil
	wms.mu.Unlock()

	// sleep until the cooldown until time
	time.Sleep(time.Until(cdu))
	deps.Disable()

	// wait until the worker is ready
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if wt.worker.managedOnMaintenanceCooldown() {
			return errors.New("Worker still on maintenance cool down")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// download the data using the RHP3 download method
	err = build.Retry(60, time.Second, func() error {
		actual, err := wt.renter.DownloadByRoot(root, 0, modules.SectorSize, 0)
		if err != nil {
			return err
		}
		if !bytes.Equal(actual, data) {
			return errors.New("Downloaded data did not match uploaded data")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
