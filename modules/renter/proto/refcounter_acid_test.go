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

	rcMuRW := &sync.RWMutex{}
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

				if err = preformUpdateOperations(rc, rcMuRW); err != nil {
					if errors.Contains(err, dependencies.ErrDiskFault) {
						atomic.AddInt64(&atomicNumRecoveries, 1)
						break INNER
					}
					// If the error wasn't caused by the dependency, the
					// test fails.
					t.Fatal(err)
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
				// Reload the wal from disk
				txns, newWal, errLoad := writeaheadlog.New(walPath)
				if errLoad != nil {
					t.Fatal("Failed to load WAL from disk:", err)
				}
				// Apply unfinished txns.
				if err = applyUnfinishedTransactions(rcFilePath, txns, fdd, rcMuRW); err != nil {
					if errors.Contains(err, dependencies.ErrDiskFault) {
						atomic.AddInt64(&atomicNumRecoveries, 1)
						continue LOAD // try again
					}
					t.Fatal(err)
				}

				// Load the file again.
				rcNew, err := LoadRefCounter(rcFilePath, newWal)
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
	}

	// Run the workload on runtime.NumCPU() * 10 threads
	var wg sync.WaitGroup
	for i := 0; i < runtime.NumCPU()*10; i++ {
		wg.Add(1)
		go func() {
			workload()
			wg.Done()
		}()
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

func preformUpdateOperations(rc *RefCounter, walMuRW *sync.RWMutex) (err error) {
	walMuRW.RLock()
	defer walMuRW.RUnlock()
	// Ignore the err, as we're not going to delete the file.
	_ = rc.StartUpdate()
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
			// check if the operation is valid - we won't gain anything
			// from hitting an overflow
			secIdx := fastrand.Uint64n(rc.numSectors)
			if ok, _ := isIncrementValid(rc, secIdx); !ok {
				continue
			}
			if u, err = rc.Increment(secIdx); err != nil {
				return
			}
			fmt.Print("+")
			updates = append(updates, u)
		}
	}

	// 50% chance to decrement, 2 chances
	for i := 0; i < 2; i++ {
		if fastrand.Intn(100) < 50 {
			// check if the operation is valid - we won't gain anything
			// from hitting an underflow
			secIdx := fastrand.Uint64n(rc.numSectors)
			if ok, _ := isDecrementValid(rc, secIdx); !ok {
				continue
			}
			if u, err = rc.Decrement(secIdx); err != nil {
				return
			}
			fmt.Print("-")
			updates = append(updates, u)
		}
	}

	// 30% chance to append
	if fastrand.Intn(100) < 20 {
		if u, err = rc.Append(); err != nil {
			return
		}
		fmt.Print("^")
		updates = append(updates, u)
	}

	// 20% chance to drop up to 2 sectors
	if fastrand.Intn(100) < 20 {
		if u, err = rc.DropSectors(fastrand.Uint64n(3)); err != nil {
			return
		}
		fmt.Print("x")
		updates = append(updates, u)
	}

	// 20% chance to swap sectors
	if fastrand.Intn(100) < 20 {
		var us []writeaheadlog.Update
		if us, err = rc.Swap(fastrand.Uint64n(rc.numSectors), fastrand.Uint64n(rc.numSectors)); err != nil {
			return
		}
		fmt.Print("@")
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
	fmt.Print("|")

	return nil
}

func applyUnfinishedTransactions(filepath string, txns []*writeaheadlog.Transaction, fdd *dependencies.DependencyFaultyDisk, rcMuRW *sync.RWMutex) error {
	rcMuRW.Lock()
	defer rcMuRW.Unlock()
	f, err := fdd.OpenFile(filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "failed to open refcounter file in order to apply updates")
	}
	defer f.Close()
	for _, txn := range txns {
		if err := applyUpdates(f, txn.Updates...); err != nil {
			return errors.AddContext(err, "failed to apply updates")
		}
		if err := txn.SignalUpdatesApplied(); err != nil {
			return errors.AddContext(err, "failed to signal updates applied")
		}
	}
	return f.Sync()
}
