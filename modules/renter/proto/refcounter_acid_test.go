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
	fdd.Disable()
	rc.deps = fdd

	// The outer loop is responsible for simulating a restart of siad by
	// reloading the wal, applying transactions and loading the sf from disk
	// again.
	fdd.Enable()
	atomicNumRecoveries := int64(0)
	atomicNumSuccessfulIterations := int64(0)

	// We'll use rcMu in order to control access to our refcounter
	var wg sync.WaitGroup
	workload := func(rcLocal *RefCounter) {
		testDone := time.After(testTimeout / 2)
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

				// Start an update session for this run of the for loop.
				// Ignore the err, as we're not going to delete the file.
				_ = rcLocal.StartUpdate()
				updates := make([]writeaheadlog.Update, 0)
				var u writeaheadlog.Update

				//  - 50% chance to increment, 2 chances
				for i := 0; i < 2; i++ {
					if fastrand.Intn(100) < 50 {
						// check if the operation is ok - we won't gain anything
						// from hitting an overflow
						secNum := fastrand.Uint64n(rcLocal.numSectors)
						ok, err := isIncrementValid(rcLocal, secNum)
						if err != nil {
							t.Fatal(err)
						}
						if !ok {
							continue
						}
						if u, err = rcLocal.Increment(fastrand.Uint64n(rcLocal.numSectors)); err != nil {
							if errors.Contains(err, dependencies.ErrDiskFault) {
								atomic.AddInt64(&atomicNumRecoveries, 1)
								rcLocal.UpdateApplied() // break the update session
								break INNER
							}
							// If the error wasn't caused by the dependency, the
							// test fails.
							t.Fatal(err)
						}
						updates = append(updates, u)
						fmt.Print("+") // DEBUG
					}
				}

				//  - 50% chance to decrement, 2 chances
				for i := 0; i < 2; i++ {
					if fastrand.Intn(100) < 50 {
						// check if the operation is ok - we won't gain anything
						// from hitting an underflow
						secNum := fastrand.Uint64n(rcLocal.numSectors)
						ok, err := isDecrementValid(rcLocal, secNum)
						if err != nil {
							t.Fatal(err)
						}
						if !ok {
							continue
						}
						if u, err = rcLocal.Decrement(secNum); err != nil {
							if errors.Contains(err, dependencies.ErrDiskFault) {
								atomic.AddInt64(&atomicNumRecoveries, 1)
								rcLocal.UpdateApplied() // break the update session
								break INNER
							}
							// If the error wasn't caused by the dependency, the
							// test fails.
							t.Fatal(err)
						}
						updates = append(updates, u)
						fmt.Print("-") // DEBUG
					}
				}

				//  - 20% chance to append
				if fastrand.Intn(100) < 20 {
					if u, err = rcLocal.Append(); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							rcLocal.UpdateApplied() // break the update session
							break INNER
						}
						// If the error wasn't caused by the dependency, the
						// test fails.
						t.Fatal(err)
					}
					updates = append(updates, u)
					fmt.Print("^") // DEBUG
				}

				//  - 20% chance to drop a sector
				if fastrand.Intn(100) < 20 {
					// we can't drop more sectors than we have - ignore
					if rc.numSectors == 0 {
						continue
					}
					if u, err = rcLocal.DropSectors(1); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							rcLocal.UpdateApplied() // break the update session
							break INNER
						}
						// If the error wasn't caused by the dependency, the
						// test fails.
						t.Fatal(err)
					}
					updates = append(updates, u)
					fmt.Print("x") // DEBUG
				}

				// Finish the update session.
				if len(updates) > 0 {
					if err = rcLocal.CreateAndApplyTransaction(updates...); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							break
						}
						// If the error wasn't caused by the dependency, the test
						// fails.
						t.Fatal(err)
					}
				}
				rcLocal.UpdateApplied()
				atomic.AddInt64(&atomicNumSuccessfulIterations, 1)
			}

			// 20% chance that drive is repaired.
			if fastrand.Intn(100) < 20 {
				fdd.Reset()
			}

			// Try to reload the file. This simulates failures during recovery.
		LOAD:
			for tries := 0; ; tries++ {
				// If we have already tried for 10 times, we Reset the dependency
				// to avoid getting stuck here.
				if tries%10 == 0 {
					fdd.Reset()
				}
				// Close existing wal.
				_, err := wal.CloseIncomplete()
				if err != nil {
					t.Fatal(err)
				}
				// Reopen wal.
				var txns []*writeaheadlog.Transaction
				txns, wal, err = writeaheadlog.New(walPath)
				if err != nil {
					t.Fatal(err)
				}
				// Apply unfinished txns.
				f, err := rcLocal.deps.OpenFile(rcLocal.filepath, os.O_RDWR, modules.DefaultFilePerm)
				if err != nil {
					t.Fatal("Failed to open refcounter file in order to apply updates:", err)
				}
				for _, txn := range txns {
					if err := applyUpdates(f, txn.Updates...); err != nil {
						if errors.Contains(err, dependencies.ErrDiskFault) {
							atomic.AddInt64(&atomicNumRecoveries, 1)
							f.Close()
							continue LOAD // try again
						} else {
							f.Close()
							t.Fatal(err)
						}
					}
					if err := txn.SignalUpdatesApplied(); err != nil {
						f.Close()
						t.Fatal(err)
					}
				}
				f.Close()

				// Load file again.
				rcNew, err := LoadRefCounter(rcLocal.filepath, wal)
				if err != nil {
					if errors.Contains(err, dependencies.ErrDiskFault) {
						atomic.AddInt64(&atomicNumRecoveries, 1)
						continue // try again
					} else {
						t.Fatal(err)
					}
				}
				rcLocal = &rcNew
				rcLocal.deps = fdd
				break
			}
		}
		wg.Done()
	}

	// Run the workload on runtime.NumCPU() * 10 threads
	wg.Add(runtime.NumCPU() * 10)
	for i := 0; i < runtime.NumCPU()*10; i++ {
		go workload(&rc)
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
