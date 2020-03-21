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

	// test our ability to reliably read the correct value of a counter
	if err = testReadCount(&rc); err != nil {
		t.Fatal(errors.AddContext(err, "Failed to test readCount"))
	}

	// test our ability to reliably create, read, and execute WAL updates
	if err = testWALFunctions(); err != nil {
		t.Fatal(err)
	}

	var u writeaheadlog.Update
	// make sure we cannot create updates outside of an update session
	if _, err = rc.Append(); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent an append update creation outside an update session", err)
	}
	if _, err = rc.Decrement(1); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent a decrement update creation outside an update session", err)
	}
	if _, err = rc.DeleteRefCounter(); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent a delete update creation outside an update session", err)
	}
	if _, err = rc.DropSectors(1); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent a truncate update creation outside an update session", err)
	}
	if _, err = rc.Increment(1); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent an increment update creation outside an update session", err)
	}
	if _, err = rc.Swap(1, 2); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent a swap update creation outside an update session", err)
	}
	if err = rc.CreateAndApplyTransaction(u); err != ErrUpdateWithoutUpdateSession {
		t.Fatal("Failed to prevent a CreateAndApplyTransaction call outside an update session", err)
	}

	// testCounterVal generates a specific count value based on the given `n`
	testCounterVal := func(n uint16) uint16 {
		return n*10 + 1
	}

	// set specific counts, so we can track drift
	rc.StartUpdate()
	updates := make([]writeaheadlog.Update, testSectorsCount)
	for i := uint64(0); i < testSectorsCount; i++ {
		updates[i] = createWriteAtUpdate(rc.filepath, i, testCounterVal(uint16(i)))
	}
	if err = rc.CreateAndApplyTransaction(updates...); err != nil {
		t.Fatal("Failed to write count to disk")
	}
	rc.UpdateApplied()

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

	numSectorsBefore := rc.numSectors
	updates = make([]writeaheadlog.Update, 0)
	rc.StartUpdate()
	// test Append
	if u, err = rc.Append(); err != nil {
		t.Fatal("Failed to create an append update", err)
	}
	updates = append(updates, u)
	if u, err = rc.Append(); err != nil {
		t.Fatal("Failed to create an append update", err)
	}
	updates = append(updates, u)
	if rc.numSectors != numSectorsBefore+2 {
		t.Fatal(fmt.Errorf("Append failed to properly increase the numSectors counter. Expected %d, got %d", numSectorsBefore+2, rc.numSectors))
	}

	// test Increment on the first appended counter
	if u, err = rc.Increment(rc.numSectors - 2); err != nil {
		t.Fatal("Failed to create an increment update:", err)
	}
	updates = append(updates, u)
	// we expect the value to have increased from the base 1 to 2
	readValAfterInc, err := rc.readCount(rc.numSectors - 2)
	if err != nil {
		t.Fatal("Failed to read value after increment:", err)
	}
	if readValAfterInc != 2 {
		t.Fatal(fmt.Errorf("read wrong value after increment. Expected %d, got %d", 2, readValAfterInc))
	}

	// test Decrement on the second appended counter
	if u, err = rc.Decrement(rc.numSectors - 1); err != nil {
		t.Fatal("Failed to create decrement update:", err)
	}
	updates = append(updates, u)
	// we expect the value to have decreased from the base 1 to 0
	readValAfterDec, err := rc.readCount(rc.numSectors - 1)
	if err != nil {
		t.Fatal("Failed to read value after decrement:", err)
	}
	if readValAfterDec != 0 {
		t.Fatal(fmt.Errorf("read wrong value after increment. Expected %d, got %d", 0, readValAfterDec))
	}

	// test Swap
	us, err := rc.Swap(rc.numSectors-2, rc.numSectors-1)
	if err != nil {
		t.Fatal("Failed to create swap update", err)
	}
	updates = append(updates, us...)
	var valAfterSwap1, valAfterSwap2 uint16
	if valAfterSwap1, err = rc.readCount(rc.numSectors - 2); err != nil {
		t.Fatal("Failed to read value after swap", err)
	}
	if valAfterSwap2, err = rc.readCount(rc.numSectors - 1); err != nil {
		t.Fatal("Failed to read value after swap", err)
	}
	if valAfterSwap1 != 0 || valAfterSwap2 != 2 {
		t.Fatal(fmt.Errorf("read wrong value after swap. Expected %d and %d, got %d and %d", 0, 2, valAfterSwap1, valAfterSwap2))
	}

	// apply the updates and check the values again
	if err = rc.CreateAndApplyTransaction(updates...); err != nil {
		t.Fatal("Failed to apply updates", err)
	}
	rc.UpdateApplied()

	// first ensure that the temp override map is empty
	if len(rc.newSectorCounts) != 0 {
		t.Fatal(fmt.Errorf("temp override map is not empty. Expected 0 values, got %d", len(rc.newSectorCounts)))
	}
	var valAfterApply1, valAfterApply2 uint16
	if valAfterApply1, err = rc.readCount(rc.numSectors - 2); err != nil {
		t.Fatal("Failed to read value after apply", err)
	}
	if valAfterApply2, err = rc.readCount(rc.numSectors - 1); err != nil {
		t.Fatal("Failed to read value after apply", err)
	}
	if valAfterApply1 != 0 || valAfterApply2 != 2 {
		t.Fatal(fmt.Errorf("read wrong value after apply. Expected %d and %d, got %d and %d", 0, 2, valAfterApply1, valAfterApply2))
	}

	// we expect the file size to have grown by 4 bytes
	midStats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("Failed to get file stats after updates application:", err)
	}
	if midStats.Size() != stats.Size()+4 {
		t.Fatal(fmt.Sprintf("File size did not grow as expected, expected size: %d, actual size: %d", stats.Size()+4, midStats.Size()))
	}

	rc.StartUpdate()
	// test DropSectors by dropping the two counters we added
	if u, err = rc.DropSectors(2); err != nil {
		t.Fatal("Failed to create Truncate update:", err)
	}
	if rc.numSectors != numSectorsBefore {
		t.Fatal(fmt.Errorf("wrong number of counters after Truncate. Expected %d, got %d", numSectorsBefore, rc.numSectors))
	}
	// apply
	if err = rc.CreateAndApplyTransaction(u); err != nil {
		t.Fatal("Failed to apply Truncate update:", err)
	}
	rc.UpdateApplied()

	// we expect the file size to be back to the original value
	endStats, err := os.Stat(rcFilePath)
	if err != nil {
		t.Fatal("Failed to get file stats:", err)
	}
	if endStats.Size() != stats.Size() {
		t.Fatal(fmt.Sprintf("File size did not go back to the original as expected, expected size: %d, actual size: %d", stats.Size(), endStats.Size()))
	}

	// individually test LoadRefCounter
	if err = testLoad(rcFilePath, testWAL); err != nil {
		t.Fatal(err)
	}

	// load from disk
	rcLoaded, err := LoadRefCounter(rcFilePath, testWAL)
	if err != nil {
		t.Fatal("Failed to load RefCounter from disk:", err)
	}
	// make sure we have the right number of counts after the truncation
	// (nothing was truncated away that we still needed)
	if rcLoaded.numSectors != testSectorsCount {
		t.Fatal(fmt.Sprintf("Failed to load the correct number of sectors. expected %d, got %d", testSectorsCount, rc.numSectors))
	}

	// delete the ref counter
	rc.StartUpdate()
	if u, err = rc.DeleteRefCounter(); err != nil {
		t.Fatal("Failed to create a delete update", err)
	}

	// make sure we cannot create any updates after a deletion has been triggered
	if _, err = rc.Append(); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}
	if _, err = rc.Decrement(1); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}
	if _, err = rc.DeleteRefCounter(); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}
	if _, err = rc.DropSectors(1); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}
	if _, err = rc.Increment(1); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}
	if _, err = rc.Swap(1, 2); err != ErrUpdateAfterDelete {
		t.Fatal("Failed to prevent an update creation after a deletion", err)
	}

	if err = rc.CreateAndApplyTransaction(u); err != nil {
		t.Fatal("Failed to apply a delete update:", err)
	}
	rc.UpdateApplied()
	_, err = os.Stat(rcFilePath)
	if err == nil {
		t.Fatal("RefCounter deletion finished successfully but the file is still on disk", err)
	}
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

// testReadCount tests that the `readCount` method always returns the correct
// counter value, either from disk or from in-mem storage.
func testReadCount(rc *RefCounter) error {
	testSec := uint64(2) // make sure this value is below testSectorsCount
	testVal := uint16(21)
	testOverrideVal := uint16(12)
	// set up the expected value on disk
	if err := writeVal(rc.filepath, testSec, testVal); err != nil {
		return err
	}
	// verify we can read it correctly
	readVal, err := rc.readCount(testSec)
	if err != nil {
		return errors.AddContext(err, "failed to read count from disk")
	}
	if readVal != testVal {
		return fmt.Errorf("read wrong value from disk: expected %d, got %d", testVal, readVal)
	}
	// set up a temporary override
	rc.newSectorCounts[testSec] = testOverrideVal
	// verify we can read it correctly
	readOverrideVal, err := rc.readCount(testSec)
	if err != nil {
		return errors.AddContext(err, "failed to read count from disk")
	}
	if readOverrideVal != testOverrideVal {
		return fmt.Errorf("read wrong override value from disk: expected %d, got %d", testOverrideVal, readOverrideVal)
	}
	return nil
}

// testWALFunctions tests RefCounter's functions for creating and
// reading WAL updates
func testWALFunctions() error {
	// test creating and reading updates
	writtenPath := "test/writtenPath"
	writtenSec := uint64(2)
	writtenVal := uint16(12)
	writeUp := createWriteAtUpdate(writtenPath, writtenSec, writtenVal)
	readPath, readSec, readVal, err := readWriteAtUpdate(writeUp)
	if err != nil {
		return errors.AddContext(err, "failed to read WriteAt update")
	}
	if writtenPath != readPath || writtenSec != readSec || writtenVal != readVal {
		return fmt.Errorf("Wrong values read from WriteAt update. Expected %s, %d, %d, found %s, %d, %d.", writtenPath, writtenSec, writtenVal, readPath, readSec, readVal)
	}

	truncUp := createTruncateUpdate(writtenPath, writtenSec)
	readPath, readSec, err = readTruncateUpdate(truncUp)
	if err != nil {
		return errors.AddContext(err, "failed to read Truncate update")
	}
	if writtenPath != readPath || writtenSec != readSec {
		return fmt.Errorf("Wrong values read from Truncate update. Expected %s, %d found %s, %d.", writtenPath, writtenSec, readPath, readSec)
	}

	return nil
}

// writeVal is a helper method that writes a certain counter value to disk. This
// method does not do any validations or checks, the caller must make certain
// that the input parameters are valid.
func writeVal(path string, secIdx uint64, val uint16) error {
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return errors.AddContext(err, "failed to open refcounter file")
	}
	defer f.Close()
	var b u16
	binary.LittleEndian.PutUint16(b[:], val)
	if _, err = f.WriteAt(b[:], int64(offset(secIdx))); err != nil {
		return errors.AddContext(err, "failed to write to refcounter file")
	}
	return f.Sync()
}
