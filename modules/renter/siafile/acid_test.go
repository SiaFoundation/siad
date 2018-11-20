package siafile

import (
	"fmt"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// TestSiaFileFaultyDisk simulates interacting with a SiaFile on a faulty disk.
func TestSiaFileFaultyDisk(t *testing.T) {
	// Determine a reasonable timeout for the test.
	var testTimeout time.Duration
	if testing.Short() {
		t.SkipNow()
	} else if build.VLONG {
		testTimeout = time.Minute
	} else {
		testTimeout = 10 * time.Second
	}

	// Create the dependency.
	fdd := newFaultyDiskDependency(100) // Fails after 100 writes.
	fdd.disable()

	// Create a new blank siafile.
	sf, wal, walPath := newBlankTestFileAndWAL()
	sf.deps = fdd

	// Create 50 hostkeys from which to choose from.
	hostkeys := make([]types.SiaPublicKey, 0, 50)
	for i := 0; i < 50; i++ {
		spk := types.SiaPublicKey{}
		fastrand.Read(spk.Key)
		hostkeys = append(hostkeys, types.SiaPublicKey{})
	}

	// The outer loop is responsible for simulating a restart of siad by
	// reloading the wal, applying transactions, loading the sf from disk again
	// and
	fdd.enable()
	testDone := time.After(testTimeout)
	numRecoveries := 0
OUTER:
	for {
		select {
		case <-testDone:
			fmt.Println(testTimeout.Seconds())
			break OUTER
		default:
		}

		// The inner loop applies a random number of operations on the file.
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
			// 80% chance to add a piece.
			if fastrand.Intn(100) < 80 {
				spk := hostkeys[fastrand.Intn(len(hostkeys))]
				offset := uint64(fastrand.Intn(int(sf.staticMetadata.StaticFileSize)))
				chunkIndex, _ := sf.ChunkIndexByOffset(offset)
				pieceIndex := uint64(fastrand.Intn(sf.staticMetadata.staticErasureCode.NumPieces()))
				if err := sf.AddPiece(spk, chunkIndex, pieceIndex, crypto.Hash{}); err != nil {
					if errors.Contains(err, errDiskFault) {
						numRecoveries++
						break
					}
					// If the error wasn't caused by the dependency, the test
					// fails.
					t.Fatal(err)
				}
			}
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
		for _, txn := range txns {
			if err := applyUpdates(fdd, txn.Updates...); err != nil {
				t.Fatal(err)
			}
		}
		// Load file again.
		sf, err = loadSiaFile(sf.siaFilePath, wal, fdd)
		if err != nil {
			t.Fatal(err)
		}
		sf.deps = fdd
		// TODO Additional checks on the file.
	}
	t.Logf("Recovered from %v disk failures", numRecoveries)
}
