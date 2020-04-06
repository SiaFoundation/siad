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

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// ErrTestTimeout is returned when the time allotted for testing runs out. It's
// not a real error in the sense that it doesn't cause the test to fail.
var ErrTestTimeout = errors.New("test timeout has run out")

// status allows us to manually keep track of all the changes that happen to a
// refcounter in order to validate them
type status struct {
	counts []uint16
	sync.Mutex
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
	rc, err := NewCustomRefCounter(rcFilePath, 200, wal, fdd)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}

	// Create a struct to monitor all changes happening to the test refcounter.
	// At the end we'll use it to validate all changes.
	statusTracker := &status{
		counts: make([]uint16, rc.numSectors),
	}
	for i := uint64(0); i < rc.numSectors; i++ {
		statusTracker.counts[i] = 1
	}

	// stat counters
	var atomicNumRecoveries uint64
	var atomicNumSuccessfulIterations uint64

	// Keeps applying a random number of operations on the refcounter until
	// an error occurs.
	performUpdates := func(rcLocal *RefCounter, st *status, doneChan <-chan struct{}) error {
		for {
			err := preformUpdateOperations(rcLocal, st)
			if err != nil {
				// we have an error, fake or not we should return
				return err
			}
			atomic.AddUint64(&atomicNumSuccessfulIterations, 1)

			select {
			case <-doneChan:
				return nil
			default:
			}
		}
	}

	// doneChan will signal to all goroutines that it's time to wrap up and exit
	doneChan := make(chan struct{})
	// we close the doneChan instead of sending on it so we can notify all
	// goroutines listening on it and not just one
	time.AfterFunc(testTimeout, func() {
		close(doneChan)
	})

	var wg sync.WaitGroup
	// The outer loop keeps restarting the tests until the time runs out
OUTER:
	for {
		// Run a high number of tests in parallel
		for i := 0; i < runtime.NumCPU()*10; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				errLocal := performUpdates(rc, statusTracker, doneChan)
				if errLocal != nil && !errors.Contains(errLocal, dependencies.ErrDiskFault) && !errors.Contains(errLocal, ErrTimeoutOnLock) {
					// We have a real error - fail the test
					t.Error(errLocal)
				}
			}(i)
		}
		wg.Wait()

		select {
		case <-doneChan:
			// the time has run out, finish the test
			break OUTER
		default:
			// there is still time, load the wal from disk and re-run the test
			rc, err = reloadRefCounter(rcFilePath, walPath, fdd, &atomicNumRecoveries, doneChan)
			if errors.Contains(err, ErrTestTimeout) {
				break OUTER
			}
			if err != nil {
				t.Fatal("Failed to reload wal from disk:", err)
			}
		}
	}

	// Validate changes
	if rc.numSectors != uint64(len(statusTracker.counts)) {
		t.Fatalf("Expected %d sectors, got %d", uint64(len(statusTracker.counts)), rc.numSectors)
	}
	errorList := make([]error, 0)
	for i := uint64(0); i < rc.numSectors; i++ {
		n, err := rc.readCount(i)
		if err != nil {
			t.Fatal("Failed to read count:", err)
		}
		if n != statusTracker.counts[i] {
			errorList = append(errorList, fmt.Errorf("Expected sector count value of sector %d to be %d, got %d", i, statusTracker.counts[i], n))
		}
	}
	if len(errorList) > 0 {
		t.Fatal("Sector count values do not match expectations!", errors.Compose(errorList...))
	}

	t.Logf("\nRecovered from %v disk failures\n", atomicNumRecoveries)
	t.Logf("Inner loop %v iterations without failures\n", atomicNumSuccessfulIterations)
}

// loadWal reads the wal from disk and applies all outstanding transactions
func loadWal(filepath string, walPath string, fdd *dependencies.DependencyFaultyDisk) (*writeaheadlog.WAL, error) {
	// load the wal from disk
	txns, newWal, err := writeaheadlog.New(walPath)
	if err != nil {
		return nil, errors.AddContext(err, "failed to load wal from disk")
	}
	f, err := fdd.OpenFile(filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return nil, errors.AddContext(err, "failed to open refcounter file in order to apply updates")
	}
	defer f.Close()
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

// preformUpdateOperations executes a randomised set of updates within an
// update session.
func preformUpdateOperations(rc *RefCounter, st *status) (err error) {
	err = rc.StartUpdate(100 * time.Millisecond)
	if err != nil {
		// don't fail the test on a timeout on the lock
		if errors.Contains(err, ErrTimeoutOnLock) {
			err = nil
		}
		return
	}
	defer rc.UpdateApplied()

	updates := make([]writeaheadlog.Update, 0)
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
			if u, err = rc.Increment(secIdx); err != nil {
				return
			}
			st.Lock()
			st.counts[secIdx]++
			st.Unlock()
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
			if u, err = rc.Decrement(secIdx); err != nil {
				return
			}
			st.Lock()
			st.counts[secIdx]--
			st.Unlock()
			updates = append(updates, u)
		}
	}

	// 40% chance to append
	if fastrand.Intn(100) < 40 {
		if u, err = rc.Append(); err != nil {
			return
		}
		st.Lock()
		st.counts = append(st.counts, 1)
		st.Unlock()
		updates = append(updates, u)
	}

	// 20% chance to drop up to 2 sectors
	if fastrand.Intn(100) < 20 {
		secNum := fastrand.Uint64n(3)
		// check if the operation is valid - we won't gain anything
		// from running out of sectors
		if errValidate := validateDropSectors(rc, secNum); errValidate == nil {
			if u, err = rc.DropSectors(secNum); err != nil {
				return
			}
			st.Lock()
			st.counts = st.counts[:len(st.counts)-int(secNum)]
			st.Unlock()
			updates = append(updates, u)
		}
	}

	// 20% chance to swap sectors
	if fastrand.Intn(100) < 20 {
		var us []writeaheadlog.Update
		secIdx1 := fastrand.Uint64n(rc.numSectors)
		secIdx2 := fastrand.Uint64n(rc.numSectors)
		if us, err = rc.Swap(secIdx1, secIdx2); err != nil {
			return
		}
		st.Lock()
		st.counts[secIdx1], st.counts[secIdx2] = st.counts[secIdx2], st.counts[secIdx1]
		st.Unlock()
		updates = append(updates, us...)
	}

	if len(updates) > 0 {
		err = rc.CreateAndApplyTransaction(updates...)
	}
	return
}

// reloadRefCounter tries to reload the file. This simulates failures during recovery.
func reloadRefCounter(rcFilePath, walPath string, fdd *dependencies.DependencyFaultyDisk, atomicNumRecoveries *uint64, doneChan <-chan struct{}) (*RefCounter, error) {
	// Try to reload the file. This simulates failures during recovery.
	for tries := 1; ; tries++ {
		select {
		case <-doneChan:
			return nil, ErrTestTimeout
		default:
		}

		// 20% chance to auto-recover
		if fastrand.Intn(100) < 20 {
			fdd.Reset()
		}

		// Every 10 times, we reset the dependency to avoid getting
		// stuck here.
		if tries%10 == 0 {
			fdd.Reset()
		}
		// Reload the wal from disk and apply unfinished txns
		newWal, err := loadWal(rcFilePath, walPath, fdd)
		if errors.Contains(err, dependencies.ErrDiskFault) {
			atomic.AddUint64(atomicNumRecoveries, 1)
			continue // try again
		} else if err != nil {
			// an actual error occurred, the test must fail
			return nil, err
		}
		// Reload the refcounter from disk
		newRc, err := LoadRefCounter(rcFilePath, newWal)
		if errors.Contains(err, dependencies.ErrDiskFault) {
			atomic.AddUint64(atomicNumRecoveries, 1)
			continue // try again
		} else if err != nil {
			// an actual error occurred, the test must fail
			return nil, err
		}
		newRc.staticDeps = fdd
		return &newRc, nil
	}
}

// validateDecrement is a helper method that returns false if the counter is 0.
// This allows us to avoid a counter underflow.
func validateDecrement(rc *RefCounter, secNum uint64) error {
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

// validateDropSectors is a helper method that returns false if the number of
// sectors in the refcounter is smaller than the number of sectors we want to
// drop.
func validateDropSectors(rc *RefCounter, secNum uint64) error {
	if rc.numSectors < secNum {
		return errors.New("Cannot drop more sectors than the total")
	}
	return nil
}

// validateDecrement is a helper method that returns false if the counter has
// reached its maximum value. This allows us to avoid a counter overflow.
func validateIncrement(rc *RefCounter, secNum uint64) error {
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
