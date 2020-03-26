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

// TestRefCounterFaultyDisk simulates interacting with a SiaFile on a faulty disk.
func TestRefCounterFaultyDisk(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Determine a reasonable timeout for the test.
	var testTimeout time.Duration
	if testing.Short() {
		t.SkipNow()
	} else if build.VLONG {
		testTimeout = 30 * time.Second
	} else {
		testTimeout = 5 * time.Second
	}

	// Prepare for the tests
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(123)
	testDir := build.TempDir(t.Name())
	wal, walPath := newTestWAL()
	if err := os.MkdirAll(testDir, modules.DefaultDirPerm); err != nil {
		t.Fatal("Failed to create test directory:", err)
	}
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)
	// Create a new ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount, wal)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}

	// Create the dependency.
	fdd := dependencies.NewFaultyDiskDependency(10000) // Fails after 10000 writes.
	rc.deps = fdd
	atomicNumRecoveries := int64(0)
	atomicNumSuccessfulIterations := int64(0)

	var walMu sync.Mutex // controls the wal reloads
	var wg sync.WaitGroup

	workload := func() {
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
					break
				}

				// 50% chance to increment, 2 chances
				for i := 0; i < 2; i++ {
					if fastrand.Intn(100) < 50 {
						if err = performIncrement(rc); err != nil {
							if errors.Contains(err, dependencies.ErrDiskFault) {
								atomic.AddInt64(&atomicNumRecoveries, 1)
								break INNER
							}
							// If the error wasn't caused by the dependency, the
							// test fails.
							t.Fatal(err)
						}
					}
				}

				// 50% chance to decrement, 2 chances
				for i := 0; i < 2; i++ {
					if fastrand.Intn(100) < 50 {
						if err = performDecrement(rc); err != nil {
							if errors.Contains(err, dependencies.ErrDiskFault) {
								atomic.AddInt64(&atomicNumRecoveries, 1)
								break INNER
							}
							// If the error wasn't caused by the dependency, the
							// test fails.
							t.Fatal(err)
						}
					}
				}

				// 20% chance to append
				if fastrand.Intn(100) < 20 {
					if err = performAppend(rc); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							break INNER
						}
						// If the error wasn't caused by the dependency, the
						// test fails.
						t.Fatal(err)
					}
				}

				// 20% chance to drop a sector
				if fastrand.Intn(100) < 20 {
					if err = performDropSector(rc); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							break INNER
						}
						// If the error wasn't caused by the dependency, the
						// test fails.
						t.Fatal(err)
					}
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
				// If we have already tried for 10 times, we reset the
				// dependency to avoid getting stuck here.
				if tries%10 == 0 {
					fdd.Reset()
				}
				walMu.Lock()
				// Close existing wal.
				if _, err := wal.CloseIncomplete(); err != nil {
					t.Fatal(err)
				}
				// Reopen wal.
				var txns []*writeaheadlog.Transaction
				txns, wal, errLoad := writeaheadlog.New(walPath)
				if errLoad != nil {
					t.Fatal(errLoad)
				}
				walMu.Unlock()
				// Apply unfinished txns.
				rc.StartUpdate()
				f, errLoad := rc.deps.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
				if errLoad != nil {
					t.Fatal("Failed to open refcounter file in order to apply updates:", errLoad)
				}
				for _, txn := range txns {
					if err := applyUpdates(f, txn.Updates...); err != nil {
						rc.UpdateApplied()
						_ = f.Close()
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							continue LOAD // try again
						} else {
							t.Fatal(err)
						}
					}
					if err := txn.SignalUpdatesApplied(); err != nil {
						_ = f.Close()
						t.Fatal(err)
					}
				}
				rc.UpdateApplied()
				_ = f.Close()

				// Load the file again.
				rcNew, err := LoadRefCounter(rc.filepath, wal)
				if err != nil {
					if errors.Contains(err, dependencies.ErrDiskFault) {
						atomic.AddInt64(&atomicNumRecoveries, 1)
						continue // try again
					} else {
						t.Fatal(err)
					}
				}
				rcNew.deps = fdd
				rc = &rcNew
				break
			}
		}
		wg.Done()
	}

	// Run the workload on runtime.NumCPU() * 10 threads
	wg.Add(runtime.NumCPU() * 10)
	for i := 0; i < runtime.NumCPU()*10; i++ {
		go workload()
	}
	wg.Wait()

	t.Logf("Recovered from %v disk failures", atomicNumRecoveries)
	t.Logf("Inner loop %v iterations without failures", atomicNumSuccessfulIterations)
}

func isDecrementValid(rc *RefCounter, secNum uint64) (bool, error) {
	n, err := rc.Count(secNum)
	// Ignore errors coming from the dependency for this one
	if err != nil && !errors.Contains(err, dependencies.ErrDiskFault) {
		// If the error wasn't caused by the dependency, the test fails.
		return false, err
	}
	return n > 0, nil
}

func isIncrementValid(rc *RefCounter, secNum uint64) (bool, error) {
	n, err := rc.Count(secNum)
	// Ignore errors coming from the dependency for this one
	if err != nil && !errors.Contains(err, dependencies.ErrDiskFault) {
		// If the error wasn't caused by the dependency, the test fails.
		return false, err
	}
	return n < math.MaxUint16, nil
}

func performIncrement(rc *RefCounter) error {
	// Ignore the err, as we're not going to delete the file.
	_ = rc.StartUpdate()
	// check if the operation is valid - we won't gain anything
	// from hitting an overflow
	secNum := fastrand.Uint64n(rc.numSectors)
	ok, err := isIncrementValid(rc, secNum)
	if err != nil || !ok {
		return err
	}
	update, err := rc.Increment(secNum)
	if err != nil {
		return err
	}
	if err = rc.CreateAndApplyTransaction(update); err != nil {
		return err
	}
	rc.UpdateApplied()

	fmt.Print("+") // DEBUG
	return nil
}

func performDecrement(rc *RefCounter) error {
	// Ignore the err, as we're not going to delete the file.
	_ = rc.StartUpdate()
	// check if the operation is valid - we won't gain anything
	// from hitting an overflow
	secNum := fastrand.Uint64n(rc.numSectors)
	ok, err := isDecrementValid(rc, secNum)
	if err != nil || !ok {
		return err
	}
	update, err := rc.Decrement(secNum)
	if err != nil {
		return err
	}
	if err = rc.CreateAndApplyTransaction(update); err != nil {
		return err
	}
	rc.UpdateApplied()

	fmt.Print("-") // DEBUG
	return nil
}

func performAppend(rc *RefCounter) error {
	// Ignore the err, as we're not going to delete the file.
	_ = rc.StartUpdate()
	update, err := rc.Append()
	if err != nil {
		return err
	}
	if err = rc.CreateAndApplyTransaction(update); err != nil {
		return err
	}
	rc.UpdateApplied()

	fmt.Print("^") // DEBUG
	return nil
}

func performDropSector(rc *RefCounter) error {
	// Ignore the err, as we're not going to delete the file.
	_ = rc.StartUpdate()
	update, err := rc.DropSectors(1)
	if err != nil {
		return err
	}
	if err = rc.CreateAndApplyTransaction(update); err != nil {
		return err
	}
	rc.UpdateApplied()

	fmt.Print("x") // DEBUG
	return nil
}
