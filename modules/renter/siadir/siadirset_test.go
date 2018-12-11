package siadir

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/fastrand"
)

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

// TestSiaDirSetOpenClose tests that the threadCount of the siadir is
// incremented and decremented properly when Open() and Close() are called
func TestSiaDirSetOpenClose(t *testing.T) {
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

// TestUpdateSiaDirSetHealth probes the UpdateHealth method of the SiaDirSet
func TestUpdateSiaDirSetHealth(t *testing.T) {
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

	// Confirm Health is set properly
	health, stuckHealth, lastCheck := entry.Health()
	if health != DefaultDirHealth {
		t.Fatalf("Health not initialized properly, got %v expected %v", health, DefaultDirHealth)
	}
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("StuckHealth not initialized properly, got %v expected %v", stuckHealth, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealtCheckTime was not initialized properly")
	}

	// Update the health of the entry
	checkTime := time.Now()
	err = sds.UpdateHealth(siaPath, 4, 2, checkTime)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm other instance of entry is updated
	health, stuckHealth, lastCheck = entry.Health()
	if health != 4 {
		t.Fatalf("Health not initialized properly, got %v expected %v", health, 4)
	}
	if stuckHealth != 2 {
		t.Fatalf("StuckHealth not initialized properly, got %v expected %v", stuckHealth, 2)
	}
	if !lastCheck.Equal(checkTime) {
		t.Fatalf("lastHealthCheckTime not updated, got %v expected %v", lastCheck, checkTime)
	}
}

// TestOpenAndCloseWithLock probes the OpenAndLockSiaDir and
// CloseAndUnlockSiaDir methods
func TestOpenAndCloseWithLock(t *testing.T) {
	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}

	// Remember siapath
	siaPath := entry.SiaPath()

	// Close entry to start with nothing in memory
	if err = entry.Close(); err != nil {
		t.Fatal(err)
	}

	// Open the siadir and hold the siadir lock
	entry, err = sds.OpenAndLockSiaDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Open another instance of the entry
	entry2, err := sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Close the second entry
	if err = entry2.Close(); err != nil {
		t.Fatal(err)
	}

	// Close and unlock locked entry
	if err = entry.CloseAndUnlockSiaDir(); err != nil {
		t.Fatal(err)
	}
}
