package renter

import (
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/errors"
)

// TestRevisionSync is a unit test that verifies if the revision number fix is
// attempted and whether it properly resync the revision.
func TestRevisionSync(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.SkipNow()
	}

	deps := dependencies.NewDependencyDisableCommitPaymentIntent()
	wt, err := newWorkerTesterCustomDependency(t.Name(), deps)
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

	// wait until our dependency got triggered
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if deps.Occurrences() == 0 {
			return errors.New("commit payment intent not interrupted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// wait until we have a valid pricetable
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticPriceTable().staticValid() {
			return errors.New("price table not updated yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// now sleep until we have updated the price table
	time.Sleep(time.Until(w.staticPriceTable().staticUpdateTime))

	// verify the host returned an error caused by a revision mismatch
	err = build.Retry(300, 100*time.Millisecond, func() error {
		if !errCausedByRevisionMismatch(w.staticPriceTable().staticRecentErr) {
			return errors.New("Expected host to have returned an error caused by revision mismatch")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// wait until we have a valid pricetable
	err = build.Retry(100, 100*time.Millisecond, func() error {
		if !w.staticPriceTable().staticValid() {
			return errors.New("price table not updated yet")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// if we reach this point we have verified that the host returns a revision
	// mismatch error and that we can successfully recover from it
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
