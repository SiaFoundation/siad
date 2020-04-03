package proto

import (
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

var ErrTestTimeout = errors.New("test timeout has run out")

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
	// attach it to the refcounter
	rc, err := NewCustomRefCounter(rcFilePath, 200, wal, fdd)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}

	// stat counters
	var atomicNumRecoveries uint64
	var atomicNumSuccessfulIterations uint64

	// Keeps applying a random number of operations on the refcounter until
	// an error occurs.
	performUpdates := func(rcLocal *RefCounter, testDone <-chan time.Time) error {
		for {
			err := preformUpdateOperations(rcLocal)
			if err != nil {
				// we have an error, fake or not we should return
				return err
			}
			atomic.AddUint64(&atomicNumSuccessfulIterations, 1)

			select {
			case <-testDone:
				return nil
			default:
			}
		}
	}

	testDone := time.After(testTimeout)
	var wg sync.WaitGroup
	// The outer loop keeps restarting the tests until the time runs out
OUTER:
	for {
		// Run a high number of tests in parallel
		for i := 0; i < runtime.NumCPU()*10; i++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				errLocal := performUpdates(rc, testDone)
				if errLocal != nil && !errors.Contains(errLocal, dependencies.ErrDiskFault) && !errors.Contains(errLocal, ErrTimeoutOnLock) {
					// We have a real error - fail the test
					t.Error(errLocal)
				}
			}(i)
		}
		wg.Wait()

		select {
		case <-testDone:
			// the time has run out, finish the test
			break OUTER
		default:
			// there is still time, load the wal from disk and re-run the test
			rc, err = reloadRefCounter(rcFilePath, walPath, fdd, &atomicNumRecoveries, testDone)
			if errors.Contains(err, ErrTestTimeout) {
				break OUTER
			}
			if err != nil {
				t.Fatal("Failed to reload wal from disk:", err)
			}
		}
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
func preformUpdateOperations(rc *RefCounter) (err error) {
	err = rc.StartUpdate(100 * time.Millisecond)
	if err != nil {
		// don't fail the test on a timeout on the lock
		if errors.Contains(err, ErrTimeoutOnLock) {
			err = nil
		}
		return
	}
	defer rc.UpdateApplied()

	updates := []writeaheadlog.Update{}
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
			updates = append(updates, u)
		}
	}

	// 40% chance to append
	if fastrand.Intn(100) < 40 {
		if u, err = rc.Append(); err != nil {
			return
		}
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
			updates = append(updates, u)
		}
	}

	// 20% chance to swap sectors
	if fastrand.Intn(100) < 20 {
		var us []writeaheadlog.Update
		if us, err = rc.Swap(fastrand.Uint64n(rc.numSectors), fastrand.Uint64n(rc.numSectors)); err != nil {
			return
		}
		updates = append(updates, us...)
	}

	if len(updates) > 0 {
		err = rc.CreateAndApplyTransaction(updates...)
	}
	return
}

// reloadRefCounter tries to reload the file. This simulates failures during recovery.
func reloadRefCounter(rcFilePath, walPath string, fdd *dependencies.DependencyFaultyDisk, atomicNumRecoveries *uint64, testDone <-chan time.Time) (*RefCounter, error) {
	// Try to reload the file. This simulates failures during recovery.
	for tries := 1; ; tries++ {
		select {
		case <-testDone:
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
