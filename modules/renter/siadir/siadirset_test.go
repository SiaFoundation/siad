package siadir

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

// newTestSiaDirSet creates a new SiaDirSet
func newTestSiaDirSet() *SiaDirSet {
	// Create params
	dir := filepath.Join(os.TempDir(), "siadirs")
	wal, _ := newTestWAL()
	return NewSiaDirSet(dir, wal)
}

// newTestSiaDirSetWithDir creates a new SiaDirSet and SiaDir and makes sure
// that they are linked
func newTestSiaDirSetWithDir() (*SiaDirSetEntry, *SiaDirSet, error) {
	// Create params
	siaPath := string(hex.EncodeToString(fastrand.Bytes(8)))
	dir := filepath.Join(os.TempDir(), "siadirs")
	// Create SiaDirSet and SiaDirSetEntry
	wal, _ := newTestWAL()
	sds := NewSiaDirSet(dir, wal)
	entry, err := sds.NewSiaDir(siaPath)
	if err != nil {
		return nil, nil, err
	}
	return entry, sds, nil
}

// TestInitRootDir checks that InitRootDir creates a siadir on disk and that it
// can be called again without returning an error
func TestInitRootDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create new SiaDirSet
	sds := newTestSiaDirSet()

	// Create a root SiaDirt
	if err := sds.InitRootDir(); err != nil {
		t.Fatal(err)
	}

	// Verify the siadir exists on disk
	siaPath := filepath.Join(sds.rootDir, SiaDirExtension)
	_, err := os.Stat(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the siadir is not stored in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("SiaDirSet has siadirs in memory")
	}

	// Try initializing the root directory again, there should be no error
	if err := sds.InitRootDir(); err != nil {
		t.Fatal(err)
	}
}

// TestSiaDirSetOpenClose tests that the threadCount of the siadir is
// incremented and decremented properly when Open() and Close() are called
func TestSiaDirSetOpenClose(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm dir is in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatalf("Expected SiaDirSet map to be of length 1, instead is length %v", len(sds.siaDirMap))
	}

	// Confirm threadCount is incremented properly
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(entry.threadMap))
	}

	// Close SiaDirSetEntry
	entry.Close()

	// Confirm that threadCount was decremented
	if len(entry.threadMap) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(entry.threadMap))
	}

	// Confirm dir was removed from memory
	if len(sds.siaDirMap) != 0 {
		t.Fatalf("Expected SiaDirSet map to be empty, instead is length %v", len(sds.siaDirMap))
	}

	// Open siafile again and confirm threadCount was incremented
	entry, err = sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(entry.threadMap))
	}
}

// TestDirsInMemory confirms that files are added and removed from memory
// as expected when files are in use and not in use
func TestDirsInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Close File
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm therte are no files in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sds.siaDirMap))
	}

	// Test accessing the same dir from two separate threads
	//
	// Open dir
	entry1, err := sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Access the dir again
	entry2, err := sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Close one of the dir instances
	err = entry1.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first dir
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are no files in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sds.siaDirMap))
	}
}

// TestUpdateSiaDirSetMetadata probes the UpdateMetadata method of the SiaDirSet
func TestUpdateSiaDirSetMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm metadata is set properly
	md := entry.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Update the metadata of the entry
	checkTime := time.Now()
	metadataUpdate := md
	metadataUpdate.Health = 4
	metadataUpdate.StuckHealth = 2
	metadataUpdate.LastHealthCheckTime = checkTime
	metadataUpdate.NumStuckChunks = 5

	err = sds.UpdateMetadata(siaPath, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Check if the metadata was updated properly in memory and on disk
	md = entry.metadata
	err = equalMetadatas(md, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}
}
