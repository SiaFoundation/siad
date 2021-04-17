package contractmanager

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
	"go.sia.tech/siad/types"
)

// testDir creates a new testing dir for the contractmanager given a test's
// name.
func testDir(testName string) string {
	path := build.TempDir(modules.ContractManagerDir, testName)
	if err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest); err != nil {
		panic(err)
	}
	return path
}

// randomEntry creates a random entry for testing.
func randomEntry() (sid sectorID, overflow uint64, entry []byte) {
	fastrand.Read(sid[:])
	overflow = fastrand.Uint64n(math.MaxUint64)
	entry = newRawEntry(sid, overflow)
	return
}

// TestNewOverflowFile is a unit test for newOverflowFile, initOverflowFile and
// loadOverflowFile.
func TestNewOverflowFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	filePath := filepath.Join(dir, "overflow.dat")

	// Create the file.
	f, err := newOverflowMap(filePath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// The map should be empty.
	if len(f.entryMap) != 0 {
		t.Fatal("entry map should be empty")
	}

	// Try fetching a random sector id from the empty map to rule out a panic
	// due to a nil map or similar issues.
	var sid sectorID
	fastrand.Read(sid[:])
	_, exists := f.Overflow(sid)
	if exists {
		t.Fatal("shouldn't exist")
	}

	// FileSize should be an entry size.
	if f.fileSize != overflowMapMetadataSize {
		t.Fatal("wrong file size", f.fileSize, overflowMapEntrySize)
	}

	// Metadata should be correct.
	expectedMD := make([]byte, overflowMapMetadataSize)
	copy(expectedMD[:types.SpecifierLen], overflowMapVersion[:])
	md := make([]byte, overflowMapMetadataSize)
	_, err = f.f.ReadAt(md, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(md, expectedMD) {
		t.Log("expected", expectedMD)
		t.Log("got", md)
		t.Fatal("invalid metadata")
	}

	// Manually write a random entry.
	sid, overflow, entry := randomEntry()
	_, err = f.f.WriteAt(entry, overflowMapMetadataSize)
	if err != nil {
		t.Fatal("failed to write entry")
	}

	// Close the file and reopen it.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	f, err = newOverflowMap(filePath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// The map should have 1 element.
	if len(f.entryMap) != 1 {
		t.Fatal("entry map should have one element", len(f.entryMap))
	}

	// Check for the right element.
	e, exists := f.entryMap[sid]
	if !exists {
		t.Fatal("element doesn't exist")
	}
	if e.offset != overflowMapMetadataSize {
		t.Fatal("element has wrong offset")
	}
	if e.overflow != overflow {
		t.Fatal("element has wrong overflow")
	}

	// FileSize should be two entry sizes.
	if f.fileSize != overflowMapMetadataSize+overflowMapEntrySize {
		t.Fatal("wrong file size", f.fileSize, 2*overflowMapEntrySize)
	}

	// Corrupt first page and close file.
	_, err = f.f.WriteAt(fastrand.Bytes(overflowMapMetadataSize), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// File should not load.
	_, err = newOverflowMap(filePath, modules.ProdDependencies)
	if !errors.Contains(err, errOverflowMetadataCorrupt) {
		t.Fatal("unexpected error", err)
	}
}

// TestNewRawEntry is a unit test for newRawEntry.
func TestNewRawEntry(t *testing.T) {
	// Get random sector data.
	var sid sectorID
	fastrand.Read(sid[:])
	overflow := fastrand.Uint64n(math.MaxUint64)

	// Manually construct an entry.
	expectedEntry := make([]byte, overflowMapEntrySize)
	copy(expectedEntry[:len(sid)], sid[:])
	binary.LittleEndian.PutUint64(expectedEntry[len(sid):len(sid)+8], overflow)

	// Construct the same entry with the function. They should match.
	entry := newRawEntry(sid, overflow)
	if !bytes.Equal(entry, expectedEntry) {
		t.Fatal("entries don't match")
	}
}

// TestSetOverflow is a unit test for SetOverflow.
func TestSetOverflow(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	dir := testDir(t.Name())
	filePath := filepath.Join(dir, "overflow.dat")

	// Create the file.
	f, err := newOverflowMap(filePath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}

	// Declare a helper to verify a single entry.
	assertEntry := func(sid sectorID, overflow uint64, offset int64, rawEntry []byte, nElements int) {
		// The map should have nElements.
		if len(f.entryMap) != nElements {
			t.Fatalf("entry map should have %v elements but got %v", nElements, len(f.entryMap))
		}

		// Check for the right element.
		e, exists := f.entryMap[sid]
		if !exists {
			t.Fatal("element doesn't exist")
		}
		if e.offset != offset {
			t.Fatal("element has wrong offset", e.offset, offset)
		}
		if e.overflow != overflow {
			t.Fatal("element has wrong overflow")
		}

		// Same thing but with Overflow.
		o, exists := f.Overflow(sid)
		if !exists {
			t.Fatal("overflow value doesn't exist")
		}
		if e.overflow != o {
			t.Fatal("wrong overflow", o, e.overflow)
		}

		// FileSize should match.
		expectedFileSize := overflowMapMetadataSize + int64(nElements)*overflowMapEntrySize
		if f.fileSize != expectedFileSize {
			t.Fatal("wrong file size", f.fileSize, expectedFileSize)
		}

		// Read the entry from disk and compare it.
		readEntry := make([]byte, overflowMapEntrySize)
		_, err = f.f.ReadAt(readEntry, e.offset)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(readEntry, rawEntry) {
			t.Log(readEntry)
			t.Log(rawEntry)
			t.Fatal("entries don't match")
		}
	}

	// Create a bunch of new entries in a loop and for every added entry, update
	// each entry again.
	var entries []sectorID
	for i := 0; i < 10; i++ {
		// Add a random entry.
		sid, overflow, rawEntry := randomEntry()
		err = f.SetOverflow(sid, overflow)
		if err != nil {
			t.Fatal(err)
		}

		// Add it to the list of entries.
		entries = append(entries, sid)

		// Remember the expected number of elements.
		nElements := i + 1

		// Assert it.
		assertEntry(sid, overflow, overflowMapMetadataSize+int64(i*overflowMapEntrySize), rawEntry, nElements)

		// Change every entry we have added so far.
		for idx, sid := range entries {
			// Get a random update.
			_, overflow, rawEntry := randomEntry()
			copy(rawEntry, sid[:])

			// Set it.
			err = f.SetOverflow(sid, overflow)
			if err != nil {
				t.Fatal(err)
			}

			// Assert it.
			assertEntry(sid, overflow, overflowMapMetadataSize+int64((idx)*overflowMapEntrySize), rawEntry, nElements)
		}
	}

	// Close the file.
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Load the file again. The entries should be the same.
	f2, err := newOverflowMap(filePath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.entryMap) != len(f2.entryMap) {
		t.Fatal("invalid length")
	}
	for sid, entry := range f.entryMap {
		entry2, exists := f2.entryMap[sid]
		if !exists {
			t.Fatal("key doesn't exist")
		}
		if !reflect.DeepEqual(entry, entry2) {
			t.Log(entry)
			t.Log(entry2)
			t.Fatal("entries don't match")
		}
	}
}
