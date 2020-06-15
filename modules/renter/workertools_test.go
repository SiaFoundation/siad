package renter

import "testing"

// TestSuspectRevisionNumberMismatchFlag is a small unit test that verifes the
// methods involved in setting and unsetting the SuspectRevisionNumberMismatch
// flag.
func TestSuspectRevisionNumberMismatchFlag(t *testing.T) {
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
	if wt.staticSuspectRevisionNumberMismatch() {
		t.Fatal("Unexpected outcome")
	}

	// set the flag and verify that it's set
	wt.staticSetSuspectRevisionNumberMismatch()
	if !wt.staticSuspectRevisionNumberMismatch() {
		t.Fatal("Unexpected outcome")
	}

	// trigger the method that tries to fix the mismatch and verify it properly
	// unsets the flag
	wt.managedTryFixRevisionNumberMismatch()
	if wt.staticSuspectRevisionNumberMismatch() {
		t.Fatal("Unexpected outcome")
	}
}
