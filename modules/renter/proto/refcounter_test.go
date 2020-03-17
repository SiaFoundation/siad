package proto

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"

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
	callsToTruncate := uint64(0)
	testDir := build.TempDir(t.Name())
	testWAL, _ := newTestWAL()
	if err := os.MkdirAll(testDir, modules.DefaultDirPerm); err != nil {
		t.Fatal("Failed to create test directory:", err)
	}
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)

	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount, testWAL)
	if err != nil {
		t.Fatal("Failed to create a reference counter:", err)
	}
	stats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("RefCounter creation finished successfully but the file is not accessible:", err)
	}

	// testCounterVal generates a specific count value based on the given `n`
	testCounterVal := func(n uint16) uint16 {
		return n*10 + 1
	}

	// set specific counts, so we can track drift
	b := make([]byte, testSectorsCount*2)
	for i := uint64(0); i < testSectorsCount; i++ {
		binary.LittleEndian.PutUint16(b[i*2:i*2+2], testCounterVal(uint16(i)))
	}
	updateCounters := writeaheadlog.WriteAtUpdate(rc.filepath, RefCounterHeaderSize, b)
	if err = rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, updateCounters); err != nil {
		t.Fatal("Failed to write count to disk")
	}

	// verify the counts we wrote
	for i := uint64(0); i < testSectorsCount; i++ {
		c, err := rc.readCount(i)
		if err != nil {
			t.Fatal("Failed to read count from disk")
		}
		if c != testCounterVal(uint16(i)) {
			t.Fatal(fmt.Sprintf("Read the wrong value form disk: expect %d, got %d", testCounterVal(uint16(i)), c))
		}
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
	count, err = rc.Increment(3)
	if err != nil {
		t.Fatal("Failed to increment the count:", err)
	}
	if count != testCounterVal(3)+1 {
		emsg := fmt.Sprintf("Wrong count returned on Increment, expected %d, got %d:", testCounterVal(3)+1, count)
		t.Fatal(emsg)
	}

	// decrement
	count, err = rc.Decrement(5)
	if err != nil {
		t.Fatal("Failed to decrement the count:", err)
	}
	if count != testCounterVal(5)-1 {
		emsg := fmt.Sprintf("Wrong count returned on Decrement, expected %d, got %d:", testCounterVal(5)-1, count)
		t.Fatal(emsg)
	}

	// individually test Swap and DropSectors
	if err = testCallSwap(&rc); err != nil {
		t.Fatal(err)
	}
	if err = testCallDropSectors(&rc, 4); err != nil {
		t.Fatal(err)
	}
	callsToTruncate += 4

	// test append
	if err = testCallAppend(&rc); err != nil {
		t.Fatal(err)
	}
	callsToTruncate--

	// decrement to zero
	count = 1
	for count > 0 {
		count, err = rc.Decrement(1)
		if err != nil {
			t.Fatal(fmt.Sprintf("Error while decrementing (current count: %d):", count), err)
		}
	}

	// swap and truncate
	if err = rc.Swap(1, testSectorsCount-callsToTruncate-1); err != nil {
		t.Fatal("Failed to swap:", err)
	}
	if err = rc.DropSectors(1); err != nil {
		t.Fatal("Failed to truncate:", err)
	}
	callsToTruncate++
	// we expect the file size to have shrunk with 2 bytes
	newStats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("Failed to get file stats:", err)
	}
	if newStats.Size() != stats.Size()-int64(2*callsToTruncate) {
		t.Fatal(fmt.Sprintf("File size did not shrink as expected, expected size: %d, actual size: %d", stats.Size()-int64(2*callsToTruncate), newStats.Size()))
	}
	if testSectorsCount-callsToTruncate != rc.numSectors {
		t.Fatal("Desync between rc.numSectors and the real number of sectors")
	}

	// individually test LoadRefCounter
	if err = testLoad(rcFilePath, testWAL); err != nil {
		t.Fatal(err)
	}

	// TODO: add tests for unfinished WAL updates, failing to load the WAL from disk, etc.

	// load from disk
	rcLoaded, err := LoadRefCounter(rcFilePath, testWAL)
	if err != nil {
		t.Fatal("Failed to load RefCounter from disk:", err)
	}

	// make sure we have the right number of counts after the truncation
	// (nothing was truncated away that we still needed)
	if _, err = rcLoaded.readCount(testSectorsCount - callsToTruncate - 1); err != nil {
		t.Fatal("Failed to read the last sector - wrong number of sectors truncated")
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

// testCallAppend specifically tests the testCallAppend method available outside
// the subsystem
func testCallAppend(rc *RefCounter) error {
	fiBefore, err := os.Stat(rc.filepath)
	if err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	numSectorsDiskBefore := uint64((fiBefore.Size() - RefCounterHeaderSize) / 2)
	inMemSecCountBefore := rc.numSectors
	if err := rc.Append(); err != nil {
		return errors.AddContext(err, "failed to execute the append operation")
	}
	fiAfter, err := os.Stat(rc.filepath)
	if err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	numSectorsDiskAfter := uint64((fiAfter.Size() - RefCounterHeaderSize) / 2)
	inMemSecCountAfter := rc.numSectors
	if numSectorsDiskBefore+1 != numSectorsDiskAfter {
		return fmt.Errorf(fmt.Sprintf("failed to append data on disk by one sector. sectors before %d, sectors after %d", numSectorsDiskBefore, numSectorsDiskAfter))
	}
	if inMemSecCountBefore+1 != inMemSecCountAfter {
		return fmt.Errorf("failed to update the in-memory cache of the number of secotrs")
	}
	return nil
}

// testCallSwap specifically tests the Swap method available outside
// the subsystem
func testCallSwap(rc *RefCounter) error {
	// these hold the values we expect to find at positions 2 and 4 after the swap
	expectedCount2, err := rc.readCount(4)
	if err != nil {
		return err
	}
	expectedCount4, err := rc.readCount(2)
	if err != nil {
		return err
	}
	if err = rc.Swap(2, 4); err != nil {
		return err
	}

	// check via the methods
	newCount4, err := rc.readCount(4)
	if err != nil {
		return err
	}
	newCount2, err := rc.readCount(2)
	if err != nil {
		return err
	}
	if expectedCount4 != newCount4 || expectedCount2 != newCount2 {
		return errors.New("failed to swap counts in memory")
	}

	// check directly on disk
	f, err := os.Open(rc.filepath)
	if err != nil {
		return err
	}
	defer f.Close()
	var buf u16
	if _, err := f.ReadAt(buf[:], int64(offset(4))); err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	if expectedCount4 != binary.LittleEndian.Uint16(buf[:]) {
		return errors.New("failed to swap counts on disk")
	}
	if _, err := f.ReadAt(buf[:], int64(offset(2))); err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	if expectedCount2 != binary.LittleEndian.Uint16(buf[:]) {
		return errors.New("failed to swap counts on disk")
	}
	return nil
}

// testCallDropSectors specifically tests the DropSectors method available outside
// the subsystem
func testCallDropSectors(rc *RefCounter, numSecs uint64) error {
	fiBefore, err := os.Stat(rc.filepath)
	if err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	numSectorsDiskBefore := uint64((fiBefore.Size() - RefCounterHeaderSize) / 2)
	inMemSecCountBefore := rc.numSectors
	if err := rc.DropSectors(numSecs); err != nil {
		return err
	}
	fiAfter, err := os.Stat(rc.filepath)
	if err != nil {
		return errors.AddContext(err, "failed to read from disk")
	}
	numSectorsDiskAfter := uint64((fiAfter.Size() - RefCounterHeaderSize) / 2)
	inMemSecCountAfter := rc.numSectors
	if numSectorsDiskBefore-numSecs != numSectorsDiskAfter {
		return fmt.Errorf("failed to truncate data on disk by %d sectors. Sectors before: %d, sectors after: %d", numSecs, numSectorsDiskBefore, numSectorsDiskAfter)
	}
	if inMemSecCountBefore-numSecs != inMemSecCountAfter {
		return fmt.Errorf("failed to update the in-memory cache of the number of secotrs")
	}
	return nil
}

// testLoad specifically tests LoadRefCounter and its various failure modes
func testLoad(validFilePath string, wal *writeaheadlog.WAL) error {
	// happy case
	_, err := LoadRefCounter(validFilePath, wal)
	if err != nil {
		return err
	}

	// fails with os.ErrNotExist for a non-existent file
	_, err = LoadRefCounter("there-is-no-such-file.rc", wal)
	if !errors.IsOSNotExist(err) {
		return errors.AddContext(err, "expected os.ErrNotExist, got something else")
	}

	// fails with ErrInvalidVersion when trying to load a file with a different
	// version
	badVerFilePath := validFilePath + "badver"
	f, err := os.Create(badVerFilePath)
	if err != nil {
		return errors.AddContext(err, "failed to create test file")
	}
	badVerHeader := RefCounterHeader{Version: [8]byte{9, 9, 9, 9, 9, 9, 9, 9}}
	badVerCounters := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	badVerFileContents := append(serializeHeader(badVerHeader), badVerCounters...)
	_, err = f.Write(badVerFileContents)
	_ = f.Close() // close regardless of the success of the write
	if err != nil {
		return errors.AddContext(err, "failed to write to test file")
	}
	_, err = LoadRefCounter(badVerFilePath, wal)
	if !errors.Contains(err, ErrInvalidVersion) {
		return errors.AddContext(err, fmt.Sprintf("should not be able to read file with wrong version, expected `%s` error", ErrInvalidVersion.Error()))
	}

	return nil
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() (*writeaheadlog.WAL, string) {
	// Create the wal.
	walsDir := filepath.Join(os.TempDir(), "rc-wals")
	if err := os.MkdirAll(walsDir, modules.DefaultDirPerm); err != nil {
		panic(err)
	}
	walFilePath := filepath.Join(walsDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walFilePath)
	if err != nil {
		panic(err)
	}
	return wal, walFilePath
}
