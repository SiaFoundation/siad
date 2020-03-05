package proto

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// TestRefCounter tests the RefCounter type
func TestRefCounter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// prepare for the tests
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(17)
	testDir := build.TempDir(t.Name())
	if err := os.MkdirAll(testDir, 0700); err != nil {
		t.Fatal("Failed to create test directory:", err)
	}
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)

	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}
	stats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("RefCounter creation finished successfully but the file is not accessible:", err)
	}

	// set specific counts, so we can track drift
	for i := range rc.sectorCounts {
		rc.sectorCounts[i] = testCounterVal(uint16(i))
	}

	// get count
	count, err := rc.Count(2)
	if err != nil {
		t.Fatal("Failed to get the count:", err)
	}
	if count != testCounterVal(2) {
		emsg := fmt.Sprintf("Wrong count returned on Count, expected %d, got %d:", testCounterVal(2), count)
		t.Fatal(emsg)
	}

	// increment
	count, err = rc.IncrementCount(3)
	if err != nil {
		t.Fatal("Failed to increment the count:", err)
	}
	if count != testCounterVal(3)+1 {
		emsg := fmt.Sprintf("Wrong count returned on Increment, expected %d, got %d:", testCounterVal(3)+1, count)
		t.Fatal(emsg)
	}

	// decrement
	count, err = rc.DecrementCount(5)
	if err != nil {
		t.Fatal("Failed to decrement the count:", err)
	}
	if count != testCounterVal(5)-1 {
		emsg := fmt.Sprintf("Wrong count returned on Decrement, expected %d, got %d:", testCounterVal(5)-1, count)
		t.Fatal(emsg)
	}

	// decrement to zero
	count = 1
	for count > 0 {
		count, err = rc.DecrementCount(1)
		if err != nil {
			t.Fatal(fmt.Sprintf("Error while decrementing (current count: %d):", count), err)
		}
	}
	// we expect the file size to have shrunk with 2 bytes
	newStats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("Failed to get file stats:", err)
	}
	if newStats.Size() != stats.Size()-2 {
		t.Fatal(fmt.Sprintf("File size did not shrink as expected, expected size: %d, actual size: %d", stats.Size()-2, newStats.Size()))
	}

	// load from disk
	rcLoaded, err := LoadRefCounter(rcFilePath)
	if err != nil {
		t.Fatal("Failed to load RefCounter from disk:", err)
	}

	// make sure we have the right number of counts after the truncation
	// (nothing was truncated away that we still needed)
	if uint64(len(rcLoaded.sectorCounts)) != testSectorsCount-1 {
		t.Fatal(fmt.Sprintf("Wrong sector count after trucate/load, expected: %d, actual: %d", testSectorsCount-1, len(rcLoaded.sectorCounts)))
	}

	// delete the ref counter
	err = rc.DeleteRefCounter()
	if err != nil {
		t.Fatal("Failed to delete RefCounter:", err)
	}
	_, err = os.Stat(rcFilePath)
	if err == nil {
		t.Fatal("RefCounter deletion finished successfully but the file is still on disk", err)
	}
}

// testCounterVal generates a specific count value based on the given `n`
func testCounterVal(n uint16) uint16 {
	return n*10 + 1
}
