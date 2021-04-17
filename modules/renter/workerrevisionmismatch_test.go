package renter

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
)

// TestRevisionSync is a unit test that verifies if the revision number fix is
// attempted and whether it properly resync the revision.
func TestRevisionSync(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.SkipNow()
	}

	// create a worker with a dependency that causes a revision number mismatch
	deps := dependencies.NewDependencyDisableCommitPaymentIntent()
	wt, err := newWorkerTesterCustomDependency(t.Name(), deps, modules.ProdDependencies)
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

	// verify the host returned a revision mismatch error
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if !errCausedByRevisionMismatch(w.managedMaintenanceRecentError()) {
			return fmt.Errorf("Expected host to have returned an error caused by revision mismatch, instead err was '%v'", w.managedMaintenanceRecentError())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// disable the dependency
	deps.Disable()

	// wait for the worker to come out of maintenance
	err = build.Retry(600, 100*time.Millisecond, func() error {
		if !w.managedMaintenanceSucceeded() {
			w.staticWake()
			return errors.New("Worker is still in maintenance")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// if we reach this point we know we successfully triggered a revision
	// mismatch, and the worker was able to successfully recover from it, this
	// can only mean the revision sync succeeded
}

// TestSuspectRevisionMismatchFlag is a small unit test that verifes the methods
// involved in setting and unsetting the SuspectRevisionMismatch flag.
func TestSuspectRevisionMismatchFlag(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.SkipNow()
	}

	wt, err := newWorkerTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		err := wt.Close()
		if err != nil {
			t.Fatal(err)
		}
	}()

	// check whether flag is unset
	if wt.staticSuspectRevisionMismatch() {
		t.Fatal("Unexpected outcome")
	}

	// set the flag and verify that it's set
	wt.staticSetSuspectRevisionMismatch()
	if !wt.staticSuspectRevisionMismatch() {
		t.Fatal("Unexpected outcome")
	}

	// trigger the method that tries to fix the mismatch and verify it properly
	// unsets the flag
	wt.externTryFixRevisionMismatch()
	if wt.staticSuspectRevisionMismatch() {
		t.Fatal("Unexpected outcome")
	}
}
