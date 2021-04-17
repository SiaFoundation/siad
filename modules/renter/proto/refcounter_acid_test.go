package proto

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/siatest/dependencies"
	"go.sia.tech/siad/types"
)

// errTestTimeout is returned when the time allotted for testing runs out. It's
// not a real error in the sense that it doesn't cause the test to fail.
var errTestTimeout = errors.New("test timeout has run out")

// tracker allows us to manually keep track of all the changes that happen to a
// refcounter in order to validate them
type tracker struct {
	// denotes whether we can create new updates or we first need to reload from
	// disk
	crashed bool
	counts  []uint16
	mu      sync.Mutex

	// stat counters
	atomicNumRecoveries           uint64
	atomicNumSuccessfulIterations uint64
}

// managedSetCrashed marks the tracker as crashed
func (t *tracker) managedSetCrashed(c bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.crashed = c
}

// managedCrashed checks if the tracker is marked as crashed
func (t *tracker) managedCrashed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.crashed
}

// TestRefCounterFaultyDisk simulates interacting with a SiaFile on a faulty disk.
func TestRefCounterFaultyDisk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Determine a reasonable timeout for the test
	testTimeout := 5 * time.Second
	if build.VLONG {
		testTimeout = 30 * time.Second
	}

	// Prepare for the tests
	testDir := build.TempDir(t.Name())
	wal, walPath := newTestWAL()
	if err := os.MkdirAll(testDir, modules.DefaultDirPerm); err != nil {
		t.Fatal("Failed to create test directory:", err)
	}
	// Create a new ref counter
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)
	// Create the faulty disk dependency
	fdd := dependencies.NewFaultyDiskDependency(10000) // Fails after 10000 writes.
	// Attach it to the refcounter
	rc, err := newCustomRefCounter(rcFilePath, 200, wal, fdd)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}

	// Create a struct to monitor all changes happening to the test refcounter.
	// At the end we'll use it to validate all changes.
	track := newTracker(rc)

	// testTimeoutChan will signal to all goroutines that it's time to wrap up and exit
	testTimeoutChan := make(chan struct{})
	time.AfterFunc(testTimeout, func() {
		close(testTimeoutChan)
	})

	var wg sync.WaitGroup
	// The outer loop keeps restarting the tests until the time runs out
OUTER:
	for {
		if track.managedCrashed() {
			t.Fatal("The tracker is crashed on start.")
		}
		// Run a high number of tests in parallel
		for i := 0; i < runtime.NumCPU()*10; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				errLocal := performUpdates(rc, track, testTimeoutChan)
				if errLocal != nil && !errors.Contains(errLocal, dependencies.ErrDiskFault) && !errors.Contains(errLocal, errTimeoutOnLock) {
					// We have a real error - fail the test
					t.Error(errLocal)
				}
			}(i)
		}
		wg.Wait()

		select {
		case <-testTimeoutChan:
			// the time has run out, finish the test
			break OUTER
		default:
			// there is still time, load the wal from disk and re-run the test
			rcFromDisk, err := reloadRefCounter(rcFilePath, walPath, fdd, track, testTimeoutChan)
			if errors.Contains(err, errTestTimeout) {
				break OUTER
			}
			if err != nil {
				t.Fatal("Failed to reload wal from disk:", err)
			}
			// we only assign it when there is no error because we need the
			// latest reference for the sanity check we do against the tracker
			// struct at the end
			rc = rcFromDisk
		}
	}

	// Load the WAL from disk and apply all outstanding txns.
	// Try until successful.
	for {
		wal, err = loadWal(rcFilePath, walPath, fdd)
		if errors.Contains(err, dependencies.ErrDiskFault) {
			// Disk has failed, all future attempts to load the wal will fail so
			// we need to reset the dependency and try again
			fdd.Reset()
			atomic.AddUint64(&track.atomicNumRecoveries, 1)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		break // successful
	}

	// Load the refcounter from disk.
	rc, err = loadRefCounter(rcFilePath, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Validate changes
	if err = validateStatusAfterAllTests(rc, track); err != nil {
		t.Fatal(err)
	}

	t.Logf("\nRecovered from %v disk failures\n", track.atomicNumRecoveries)
	t.Logf("Inner loop %v iterations without failures\n", track.atomicNumSuccessfulIterations)
}

// loadWal reads the wal from disk and applies all outstanding transactions
func loadWal(rcFilePath string, walPath string, fdd *dependencies.DependencyFaultyDisk) (_ *writeaheadlog.WAL, err error) {
	// load the wal from disk
	txns, newWal, err := writeaheadlog.New(walPath)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load wal from disk")
	}
	f, err := fdd.OpenFile(rcFilePath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "failed to open refcounter file in order to apply updates")
	}
	defer func() {
		err = errors.Compose(err, f.Close())
	}()
	// apply any outstanding transactions
	for _, txn := range txns {
		if err := applyUpdates(f, txn.Updates...); err != nil {
			return nil, errors.AddContext(err, "failed to apply updates")
		}
		if err := txn.SignalUpdatesApplied(); err != nil {
			return nil, errors.AddContext(err, "failed to signal updates applied")
		}
	}
	return newWal, f.Sync()
}

// newTracker creates a tracker instance and initialises its counts
// slice
func newTracker(rc *refCounter) *tracker {
	t := &tracker{
		counts: make([]uint16, rc.numSectors),
	}
	for i := uint64(0); i < rc.numSectors; i++ {
		c, err := rc.callCount(i)
		if err != nil {
			panic("Failed to read count from refcounter.")
		}
		t.counts[i] = c
	}
	return t
}

// performUpdates keeps applying a random number of operations on the refcounter
// until an error occurs.
func performUpdates(rcLocal *refCounter, tr *tracker, testTimeoutChan <-chan struct{}) error {
	for {
		err := performUpdateOperations(rcLocal, tr)
		if err != nil {
			// we have an error, fake or not we should return
			return err
		}
		if tr.managedCrashed() {
			return nil
		}

		atomic.AddUint64(&tr.atomicNumSuccessfulIterations, 1)

		select {
		case <-testTimeoutChan:
			return nil
		default:
		}
	}
}

// performUpdateOperations executes a randomised set of updates within an
// update session.
func performUpdateOperations(rc *refCounter, tr *tracker) (err error) {
	err = rc.managedStartUpdateWithTimeout(100 * time.Millisecond)
	if err != nil {
		// don't fail the test on a timeout on the lock
		if errors.Contains(err, errTimeoutOnLock) {
			err = nil
		}
		return
	}
	// This will wipe the temporary in-mem changes to the counters.
	// On success that's OK.
	// On error we need to crash anyway, so it's OK as well.
	defer rc.callUpdateApplied()

	// We can afford to lock the tracker because only one goroutine is
	// allowed to make changes at any given time anyway.
	tr.mu.Lock()
	defer func() {
		// If we're returning a disk error we need to crash in order to avoid
		// data corruption.
		if errors.Contains(err, dependencies.ErrDiskFault) {
			tr.crashed = true
		}
		tr.mu.Unlock()
	}()
	if tr.crashed {
		return nil
	}

	var updates []writeaheadlog.Update
	var u writeaheadlog.Update

	// 50% chance to increment, 2 chances
	for i := 0; i < 2; i++ {
		if fastrand.Intn(100) < 50 {
			secIdx := fastrand.Uint64n(rc.numSectors)
			// check if the operation is valid - we won't gain anything
			// from hitting an overflow
			if errValidate := validateIncrement(rc, secIdx); errValidate != nil {
				continue
			}
			if u, err = rc.callIncrement(secIdx); err != nil {
				return
			}
			tr.counts[secIdx]++
			updates = append(updates, u)
		}
	}

	// 50% chance to decrement, 2 chances
	for i := 0; i < 2; i++ {
		if fastrand.Intn(100) < 50 {
			secIdx := fastrand.Uint64n(rc.numSectors)
			// check if the operation is valid - we won't gain anything
			// from hitting an underflow
			if errValidate := validateDecrement(rc, secIdx); errValidate != nil {
				continue
			}
			if u, err = rc.callDecrement(secIdx); err != nil {
				return
			}
			tr.counts[secIdx]--
			updates = append(updates, u)
		}
	}

	// 40% chance to append
	if fastrand.Intn(100) < 40 {
		if u, err = rc.callAppend(); err != nil {
			return
		}
		tr.counts = append(tr.counts, 1)
		updates = append(updates, u)
	}

	// 20% chance to drop up to 2 sectors
	if fastrand.Intn(100) < 20 {
		secNum := fastrand.Uint64n(3)
		// check if the operation is valid - we won't gain anything
		// from running out of sectors
		if errValidate := validateDropSectors(rc, secNum); errValidate == nil {
			if u, err = rc.callDropSectors(secNum); err != nil {
				return
			}
			tr.counts = tr.counts[:len(tr.counts)-int(secNum)]
			updates = append(updates, u)
		}
	}

	// 20% chance to swap sectors
	if fastrand.Intn(100) < 20 {
		var us []writeaheadlog.Update
		secIdx1 := fastrand.Uint64n(rc.numSectors)
		secIdx2 := fastrand.Uint64n(rc.numSectors)
		if us, err = rc.callSwap(secIdx1, secIdx2); err != nil {
			return
		}
		tr.counts[secIdx1], tr.counts[secIdx2] = tr.counts[secIdx2], tr.counts[secIdx1]
		updates = append(updates, us...)
	}

	// In case of an error in its critical section, CreateAndApplyTransaction
	// will panic. An injected disk error can cause such a panic. We need to
	// recover from it and handle it as a normal faulty disk error within this
	// test.
	defer func() {
		r := recover()
		e, ok := r.(error)
		if ok && errors.Contains(e, dependencies.ErrDiskFault) {
			// we recovered by a panic caused by the faulty disk dependency
			err = dependencies.ErrDiskFault
		} else if r != nil {
			panic(r)
		}
	}()

	if len(updates) > 0 {
		err = rc.callCreateAndApplyTransaction(updates...)
	}
	return
}

// reloadRefCounter tries to reload the file. This simulates failures during recovery.
func reloadRefCounter(rcFilePath, walPath string, fdd *dependencies.DependencyFaultyDisk, tr *tracker, doneChan <-chan struct{}) (*refCounter, error) {
	// Try to reload the file. This simulates failures during recovery.
	for tries := 1; ; tries++ {
		select {
		case <-doneChan:
			return nil, errTestTimeout
		default:
		}

		// 20% chance to auto-recover
		if fastrand.Intn(100) < 20 {
			fdd.Reset()
		}

		// Every 10 times, we reset the dependency to avoid getting stuck here.
		if tries%10 == 0 {
			fdd.Reset()
		}
		// Reload the wal from disk and apply unfinished txns
		newWal, err := loadWal(rcFilePath, walPath, fdd)
		if errors.Contains(err, dependencies.ErrDiskFault) {
			atomic.AddUint64(&tr.atomicNumRecoveries, 1)
			continue // try again
		} else if err != nil {
			// an actual error occurred, the test must fail
			return nil, err
		}
		// Reload the refcounter from disk
		newRc, err := loadRefCounter(rcFilePath, newWal)
		if err != nil {
			return nil, err
		}
		newRc.staticDeps = fdd
		tr.managedSetCrashed(false)
		return newRc, nil
	}
}

// validateDecrement is a helper method that ensures the counter is above 0.
// This allows us to avoid a counter underflow.
func validateDecrement(rc *refCounter, secNum uint64) error {
	n, err := rc.readCount(secNum)
	// Ignore errors coming from the dependency for this one
	if err != nil && !errors.Contains(err, dependencies.ErrDiskFault) {
		// If the error wasn't caused by the dependency, the test fails.
		return err
	}
	if n < 1 {
		return errors.New("Cannot decrement a zero counter")
	}
	return nil
}

// validateDropSectors is a helper method that ensures the number of sectors we
// want to drop does not exceed the number of sectors in the refcounter.
func validateDropSectors(rc *refCounter, secNum uint64) error {
	if rc.numSectors < secNum {
		return errors.New("Cannot drop more sectors than the total")
	}
	return nil
}

// validateIncrement is a helper method that ensures the counter has not
// reached its maximum value. This allows us to avoid a counter overflow.
func validateIncrement(rc *refCounter, secNum uint64) error {
	n, err := rc.readCount(secNum)
	// Ignore errors coming from the dependency for this one
	if err != nil && !errors.Contains(err, dependencies.ErrDiskFault) {
		// If the error wasn't caused by the dependency, the test fails.
		return err
	}
	if n == math.MaxUint16 {
		return errors.New("Cannot increment a counter at max value")
	}
	return nil
}

// validateStatusAfterAllTests does the final validation of the test by
// comparing the state of the refcounter after all the test updates are applied.
func validateStatusAfterAllTests(rc *refCounter, tr *tracker) error {
	rc.mu.Lock()
	numSec := rc.numSectors
	rc.mu.Unlock()
	if numSec != uint64(len(tr.counts)) {
		return fmt.Errorf("Expected %d sectors, got %d\n", uint64(len(tr.counts)), numSec)
	}
	var errorList []error
	for i := uint64(0); i < numSec; i++ {
		n, err := rc.callCount(i)
		if err != nil {
			return errors.AddContext(err, "failed to read count")
		}
		tr.mu.Lock()
		if n != tr.counts[i] {
			errorList = append(errorList, fmt.Errorf("expected counter value for sector %d to be %d, got %d", i, tr.counts[i], n))
		}
		tr.mu.Unlock()
	}
	if len(errorList) > 0 {
		return errors.AddContext(errors.Compose(errorList...), "sector counter values do not match expectations")
	}
	return nil
}
