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
	var atomicNumRecoveries int64
	var atomicNumSuccessfulIterations int64

	workload := func(rcLocal *RefCounter) {
		testDone := time.After(testTimeout)
		// The outer loop is responsible for simulating a restart of siad by
		// reloading the wal, applying transactions and loading the refcounter
		// from disk again.
	OUTER:
		for {
			select {
			case <-testDone:
				break OUTER
			default:
			}
			// The inner loop applies a random number of operations on the file.
		INNER:
			for {
				select {
				case <-testDone:
					break OUTER
				default:
				}
				// 5% chance to break out of inner loop.
				if fastrand.Intn(100) < 5 {
					break INNER
				}

				if err := preformUpdateOperations(rcLocal); err != nil {
					if errors.Contains(err, dependencies.ErrDiskFault) {
						atomic.AddInt64(&atomicNumRecoveries, 1)
						break INNER
					}
					// If the error wasn't caused by the dependency the
					// test fails.
					t.Error(err)
					return
				}
				atomic.AddInt64(&atomicNumSuccessfulIterations, 1)
			}

			// 20% chance that drive is repaired.
			if fastrand.Intn(100) < 20 {
				fdd.Reset()
			}

			// Try to reload the file. This simulates failures during recovery.
		LOAD:
			for tries := 1; ; tries++ {
				// Every 10 times, we reset the dependency to avoid getting
				// stuck here.
				if tries%10 == 0 {
					fdd.Reset()
				}
				// Reload the wal from disk and apply unfinished txns.
				newWal, err := loadWal(rcFilePath, walPath, fdd)
				if errors.Contains(err, dependencies.ErrDiskFault) {
					atomic.AddInt64(&atomicNumRecoveries, 1)
					continue LOAD // try again
				} else if err != nil {
					t.Fatal(err)
				}
				// Load the file again.
				newRc, err := LoadRefCounter(rcFilePath, newWal)
				if errors.Contains(err, dependencies.ErrDiskFault) {
					atomic.AddInt64(&atomicNumRecoveries, 1)
					continue LOAD // try again
				} else if err != nil {
					t.Fatal(err)
				}
				newRc.staticDeps = fdd
				rcLocal = &newRc
				break LOAD
			}
		}
	}

	// Run the workload on runtime.NumCPU() * 10 threads
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*10; i++ {
		wg.Add(1)
		go func() {
			workload(rc)
			wg.Done()
		}()
	}
	wg.Wait()

	t.Logf("\nRecovered from %v disk failures\n", atomicNumRecoveries)
	t.Logf("Inner loop %v iterations without failures\n", atomicNumSuccessfulIterations)
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

// loadWal reads the wal from disk and applies all outstanding transactions
func loadWal(filepath string, walPath string, fdd *dependencies.DependencyFaultyDisk) (*writeaheadlog.WAL, error) {
	// load the wal from disk
	txns, newWal, err := writeaheadlog.New(walPath)
	if err != nil {
		return &writeaheadlog.WAL{}, errors.AddContext(err, "failed to load wal from disk")
	}
	f, err := fdd.OpenFile(filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return &writeaheadlog.WAL{}, errors.AddContext(err, "failed to open refcounter file in order to apply updates")
	}
	defer f.Close()
	// apply any outstanding transactions
	for _, txn := range txns {
		if err := applyUpdates(f, txn.Updates...); err != nil {
			return &writeaheadlog.WAL{}, errors.AddContext(err, "failed to apply updates")
		}
		if err := txn.SignalUpdatesApplied(); err != nil {
			return &writeaheadlog.WAL{}, errors.AddContext(err, "failed to signal updates applied")
		}
	}
	return newWal, f.Sync()
}

// preformUpdateOperations executes a randomised set of updates within an
// update session.
func preformUpdateOperations(rc *RefCounter) (err error) {
	err = rc.StartUpdate()
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			// if there is an error wrap up the update session, so we can start
			// a new one and carry on later
			rc.UpdateApplied()
		}
	}()

	updates := []writeaheadlog.Update{}
	var u writeaheadlog.Update

	// 50% chance to increment, 2 chances
	for i := 0; i < 2; i++ {
		if fastrand.Intn(100) < 50 {
			secIdx := fastrand.Uint64n(rc.numSectors)
			// check if the operation is valid - we won't gain anything
			// from hitting an overflow
			if err = validateIncrement(rc, secIdx); err != nil {
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
			if err = validateDecrement(rc, secIdx); err != nil {
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
		if err = validateDropSectors(rc, secNum); err == nil {
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

	if len(updates) == 0 {
		rc.UpdateApplied()
		return nil
	}
	if err = rc.CreateAndApplyTransaction(updates...); err != nil {
		return
	}
	rc.UpdateApplied()
	return nil
}
