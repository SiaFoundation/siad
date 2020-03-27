package proto

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
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

var testWAL = newTestWAL()

// TestLoad specifically tests LoadRefCounter and its various failure modes
func TestLoad(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(17)
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)

	// create a ref counter
	_, err = NewRefCounter(rcFilePath, testSectorsCount, testWAL)
	assertSuccess(err, t, "Failed to create a reference counter:")

	// happy case
	_, err = LoadRefCounter(rcFilePath, testWAL)
	assertSuccess(err, t, "Failed to load refcounter:")

	// fails with os.ErrNotExist for a non-existent file
	_, err = LoadRefCounter("there-is-no-such-file.rc", testWAL)
	if !errors.IsOSNotExist(err) {
		t.Fatal("Expected os.ErrNotExist, got something else:", err)
	}
}

// TestLoadInvalidHeader checks that loading a refcounters file with invalid
// header fails.
func TestLoadInvalidHeader(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)

	// Create a file that contains a corrupted header. This basically means
	// that the file is too short to have the entire header in there.
	f, err := os.Create(rcFilePath)
	assertSuccess(err, t, "Failed to create test file:")
	defer f.Close()

	// The version number is 8 bytes. We'll only write 4.
	_, err = f.Write(fastrand.Bytes(4))
	assertSuccess(err, t, "Failed to write to test file:")
	_ = f.Sync()

	// Make sure we fail to load from that file and that we fail with the right
	// error
	_, err = LoadRefCounter(rcFilePath, testWAL)
	assertErrorIs(err, io.EOF, t, fmt.Sprintf("Should not be able to read file with bad header, expected `%s` error, got:", io.EOF.Error()))
}

// TestLoadInvalidVersion checks that loading a refcounters file with invalid
// version fails.
func TestLoadInvalidVersion(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)

	// create a file with a header that encodes a bad version number
	f, err := os.Create(rcFilePath)
	assertSuccess(err, t, "Failed to create test file:")
	defer f.Close()

	// The first 8 bytes are the version number. Write down an invalid one
	// followed 4 counters (another 8 bytes).
	_, err = f.Write(fastrand.Bytes(16))
	assertSuccess(err, t, "Failed to write to test file:")
	_ = f.Sync()

	// ensure that we cannot load it and we return the correct error
	_, err = LoadRefCounter(rcFilePath, testWAL)
	assertErrorIs(err, ErrInvalidVersion, t, fmt.Sprintf("Should not be able to read file with wrong version, expected `%s` error, got:", ErrInvalidVersion.Error()))
}

// TestCount tests that the `Count` method always returns the correct
// counter value, either from disk or from in-mem storage.
func TestCount(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare for the tests
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(17)
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)
	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount, testWAL)
	assertSuccess(err, t, "Failed to create a reference counter:")

	testSec := uint64(2) // make sure this value is below testSectorsCount
	testVal := uint16(21)
	testOverrideVal := uint16(12)
	// set up the expected value on disk
	err = writeVal(rc.filepath, testSec, testVal)
	assertSuccess(err, t, "Failed to write a count to disk:")
	// verify we can read it correctly
	readVal, err := rc.Count(testSec)
	assertSuccess(err, t, "Failed to read count from disk:")
	if readVal != testVal {
		t.Fatal(fmt.Sprintf("read wrong value from disk: expected %d, got %d", testVal, readVal))
	}
	// check behaviour on bad sector number
	_, err = rc.Count(math.MaxInt64)
	assertErrorIs(err, ErrInvalidSectorNumber, t, "Expected ErrInvalidSectorNumber, got:")

	// set up a temporary override
	rc.newSectorCounts[testSec] = testOverrideVal
	// verify we can read it correctly
	readOverrideVal, err := rc.Count(testSec)
	assertSuccess(err, t, "Failed to read count from disk:")
	if readOverrideVal != testOverrideVal {
		t.Fatal(fmt.Sprintf("read wrong override value from disk: expected %d, got %d", testOverrideVal, readOverrideVal))
	}
}

// TestRefCounter tests the RefCounter type
func TestRefCounter(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare for the tests
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(17)
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)
	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount, testWAL)
	assertSuccess(err, t, "Failed to create a reference counter:")
	stats, err := os.Stat(rcFilePath)
	assertSuccess(err, t, "RefCounter creation finished successfully but the file is not accessible:")

	// testCounterVal generates a specific count value based on the given `n`
	testCounterVal := func(n uint16) uint16 {
		return n*10 + 1
	}

	// set specific counts, so we can track drift
	err = rc.StartUpdate()
	assertSuccess(err, t, "Failed to start an update session")
	updates := make([]writeaheadlog.Update, testSectorsCount)
	for i := uint64(0); i < testSectorsCount; i++ {
		updates[i] = createWriteAtUpdate(rc.filepath, i, testCounterVal(uint16(i)))
	}
	err = rc.CreateAndApplyTransaction(updates...)
	assertSuccess(err, t, "Failed to write count to disk")
	rc.UpdateApplied()
	// verify the counts we wrote
	for i := uint64(0); i < testSectorsCount; i++ {
		c, err := rc.readCount(i)
		assertSuccess(err, t, "Failed to read count from disk")
		if c != testCounterVal(uint16(i)) {
			t.Fatal(fmt.Sprintf("Read the wrong value form disk: expect %d, got %d", testCounterVal(uint16(i)), c))
		}
	}

	var u writeaheadlog.Update
	numSectorsBefore := rc.numSectors
	updates = make([]writeaheadlog.Update, 0)
	err = rc.StartUpdate()
	assertSuccess(err, t, "Failed to start an update session")

	// test Append
	u, err = rc.Append()
	assertSuccess(err, t, "Failed to create an append update")
	updates = append(updates, u)
	u, err = rc.Append()
	assertSuccess(err, t, "Failed to create an append update")
	updates = append(updates, u)
	if rc.numSectors != numSectorsBefore+2 {
		t.Fatal(fmt.Errorf("Append failed to properly increase the numSectors counter. Expected %d, got %d", numSectorsBefore+2, rc.numSectors))
	}

	// test Increment on the first appended counter
	u, err = rc.Increment(rc.numSectors - 2)
	assertSuccess(err, t, "Failed to create an increment update:")
	updates = append(updates, u)
	// we expect the value to have increased from the base 1 to 2
	readValAfterInc, err := rc.readCount(rc.numSectors - 2)
	assertSuccess(err, t, "Failed to read value after increment:")
	if readValAfterInc != 2 {
		t.Fatal(fmt.Errorf("read wrong value after increment. Expected %d, got %d", 2, readValAfterInc))
	}
	// check behaviour on bad sector number
	_, err = rc.Increment(math.MaxInt64)
	assertErrorIs(err, ErrInvalidSectorNumber, t, "Expected ErrInvalidSectorNumber, got:")

	// test Decrement on the second appended counter
	u, err = rc.Decrement(rc.numSectors - 1)
	assertSuccess(err, t, "Failed to create decrement update:")
	updates = append(updates, u)
	// we expect the value to have decreased from the base 1 to 0
	readValAfterDec, err := rc.readCount(rc.numSectors - 1)
	assertSuccess(err, t, "Failed to read value after decrement:")
	if readValAfterDec != 0 {
		t.Fatal(fmt.Errorf("read wrong value after increment. Expected %d, got %d", 0, readValAfterDec))
	}
	// check behaviour on bad sector number
	_, err = rc.Decrement(math.MaxInt64)
	assertErrorIs(err, ErrInvalidSectorNumber, t, "Expected ErrInvalidSectorNumber, got:")

	// test Swap
	us, err := rc.Swap(rc.numSectors-2, rc.numSectors-1)
	assertSuccess(err, t, "Failed to create swap update")
	updates = append(updates, us...)
	var valAfterSwap1, valAfterSwap2 uint16
	valAfterSwap1, err = rc.readCount(rc.numSectors - 2)
	assertSuccess(err, t, "Failed to read value after swap")
	valAfterSwap2, err = rc.readCount(rc.numSectors - 1)
	assertSuccess(err, t, "Failed to read value after swap")
	if valAfterSwap1 != 0 || valAfterSwap2 != 2 {
		t.Fatal(fmt.Errorf("read wrong value after swap. Expected %d and %d, got %d and %d", 0, 2, valAfterSwap1, valAfterSwap2))
	}
	// check behaviour on bad sector number
	_, err = rc.Swap(math.MaxInt64, 0)
	assertErrorIs(err, ErrInvalidSectorNumber, t, "Expected ErrInvalidSectorNumber, got:")

	// apply the updates and check the values again
	err = rc.CreateAndApplyTransaction(updates...)
	assertSuccess(err, t, "Failed to apply updates")
	rc.UpdateApplied()

	// first ensure that the temp override map is empty
	if len(rc.newSectorCounts) != 0 {
		t.Fatal(fmt.Errorf("temp override map is not empty. Expected 0 values, got %d", len(rc.newSectorCounts)))
	}
	var valAfterApply1, valAfterApply2 uint16
	valAfterApply1, err = rc.readCount(rc.numSectors - 2)
	assertSuccess(err, t, "Failed to read value after apply")
	valAfterApply2, err = rc.readCount(rc.numSectors - 1)
	assertSuccess(err, t, "Failed to read value after apply")
	if valAfterApply1 != 0 || valAfterApply2 != 2 {
		t.Fatal(fmt.Errorf("read wrong value after apply. Expected %d and %d, got %d and %d", 0, 2, valAfterApply1, valAfterApply2))
	}

	// we expect the file size to have grown by 4 bytes
	midStats, err := os.Stat(rcFilePath)
	assertSuccess(err, t, "Failed to get file stats after updates application:")
	if midStats.Size() != stats.Size()+4 {
		t.Fatal(fmt.Sprintf("File size did not grow as expected, expected size: %d, actual size: %d", stats.Size()+4, midStats.Size()))
	}

	err = rc.StartUpdate()
	assertSuccess(err, t, "Failed to start an update session")
	// test DropSectors by dropping the two counters we added
	u, err = rc.DropSectors(2)
	assertSuccess(err, t, "Failed to create Truncate update:")
	if rc.numSectors != numSectorsBefore {
		t.Fatal(fmt.Errorf("wrong number of counters after Truncate. Expected %d, got %d", numSectorsBefore, rc.numSectors))
	}
	// check behaviour on bad sector number
	_, err = rc.DropSectors(math.MaxInt64)
	assertErrorIs(err, ErrInvalidSectorNumber, t, "Expected ErrInvalidSectorNumber, got:")

	// apply
	err = rc.CreateAndApplyTransaction(u)
	assertSuccess(err, t, "Failed to apply Truncate update:")
	rc.UpdateApplied()

	// we expect the file size to be back to the original value
	endStats, err := os.Stat(rcFilePath)
	assertSuccess(err, t, "Failed to get file stats:")
	if endStats.Size() != stats.Size() {
		t.Fatal(fmt.Sprintf("File size did not go back to the original as expected, expected size: %d, actual size: %d", stats.Size(), endStats.Size()))
	}

	// delete the ref counter
	err = rc.StartUpdate()
	assertSuccess(err, t, "Failed to start an update session")
	u, err = rc.DeleteRefCounter()
	assertSuccess(err, t, "Failed to create a delete update")

	err = rc.CreateAndApplyTransaction(u)
	assertSuccess(err, t, "Failed to apply a delete update:")
	rc.UpdateApplied()

	_, err = os.Stat(rcFilePath)
	if err == nil {
		t.Fatal("RefCounter deletion finished successfully but the file is still on disk", err)
	}
}

// TestUpdateSessionConstraints ensures that StartUpdate() and UpdateApplied()
// enforce all applicable restrictions to update creation and execution
func TestUpdateSessionConstraints(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// prepare for the tests
	testContractID := types.FileContractID(crypto.HashBytes([]byte("contractId")))
	testSectorsCount := uint64(5)
	testDir := build.TempDir(t.Name())
	err := os.MkdirAll(testDir, modules.DefaultDirPerm)
	assertSuccess(err, t, "Failed to create test directory:")
	rcFilePath := filepath.Join(testDir, testContractID.String()+refCounterExtension)
	// create a ref counter
	rc, err := NewRefCounter(rcFilePath, testSectorsCount, testWAL)
	assertSuccess(err, t, "Failed to create a reference counter:")

	var u writeaheadlog.Update
	// make sure we cannot create updates outside of an update session
	_, err1 := rc.Append()
	_, err2 := rc.Decrement(1)
	_, err3 := rc.DeleteRefCounter()
	_, err4 := rc.DropSectors(1)
	_, err5 := rc.Increment(1)
	_, err6 := rc.Swap(1, 2)
	err7 := rc.CreateAndApplyTransaction(u)
	for i, err := range []error{err1, err2, err3, err4, err5, err6, err7} {
		if !errors.Contains(err, ErrUpdateWithoutUpdateSession) {
			t.Fatalf("err%v: expected %v but was %v", i+1, ErrUpdateWithoutUpdateSession, err)
		}
	}

	// start an update session
	err = rc.StartUpdate()
	assertSuccess(err, t, "Failed to start an update session")
	// delete the ref counter
	u, err = rc.DeleteRefCounter()
	assertSuccess(err, t, "Failed to create a delete update")
	// make sure we cannot create any updates after a deletion has been triggered
	_, err1 = rc.Append()
	_, err2 = rc.Decrement(1)
	_, err3 = rc.DeleteRefCounter()
	_, err4 = rc.DropSectors(1)
	_, err5 = rc.Increment(1)
	_, err6 = rc.Swap(1, 2)
	for i, err := range []error{err1, err2, err3, err4, err5, err6} {
		if !errors.Contains(err, ErrUpdateAfterDelete) {
			t.Fatalf("err%v: expected %v but was %v", i+1, ErrUpdateAfterDelete, err)
		}
	}

	// apply the updates and close the update session
	err = rc.CreateAndApplyTransaction(u)
	assertSuccess(err, t, "Failed to apply a delete update:")
	rc.UpdateApplied()

	// make sure we cannot start an update session on a deleted counter
	err = rc.StartUpdate()
	assertErrorIs(err, ErrUpdateAfterDelete, t, "Failed to prevent an update creation after a deletion")
}

// TestWALFunctions tests RefCounter's functions for creating and
// reading WAL updates
func TestWALFunctions(t *testing.T) {
	t.Parallel()

	// test creating and reading updates
	writtenPath := "test/writtenPath"
	writtenSec := uint64(2)
	writtenVal := uint16(12)
	writeUp := createWriteAtUpdate(writtenPath, writtenSec, writtenVal)
	readPath, readSec, readVal, err := readWriteAtUpdate(writeUp)
	assertSuccess(err, t, "Failed to read WriteAt update:")
	if writtenPath != readPath || writtenSec != readSec || writtenVal != readVal {
		t.Fatal(fmt.Sprintf("Wrong values read from WriteAt update. Expected %s, %d, %d, found %s, %d, %d.", writtenPath, writtenSec, writtenVal, readPath, readSec, readVal))
	}

	truncUp := createTruncateUpdate(writtenPath, writtenSec)
	readPath, readSec, err = readTruncateUpdate(truncUp)
	assertSuccess(err, t, "Failed to read a Truncate update:")
	if writtenPath != readPath || writtenSec != readSec {
		t.Fatal(fmt.Sprintf("Wrong values read from Truncate update. Expected %s, %d found %s, %d.", writtenPath, writtenSec, readPath, readSec))
	}
}

// assertSuccess is a helper function that fails the test with the given message
// if there is an error
func assertSuccess(err error, t *testing.T, msg string) {
	if err != nil {
		t.Fatal(msg, err)
	}
}

// assertSuccess is a helper function that fails the test with the given message
// if there is an error
func assertErrorIs(err error, baseError error, t *testing.T, msg string) {
	if !errors.Contains(err, baseError) {
		t.Fatal(msg, err)
	}
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() *writeaheadlog.WAL {
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
	return wal
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
