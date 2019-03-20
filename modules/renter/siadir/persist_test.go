package siadir

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"
)

// equalMetadatas is a helper that compares two siaDirMetadatas. If using this
// function to check persistence the time fields should be checked in the test
// itself as well and reset due to how time is persisted
func equalMetadatas(md, md2 Metadata) error {
	// Check AggregateNumFiles
	if md.AggregateNumFiles != md2.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md.AggregateNumFiles, md2.AggregateNumFiles)
	}
	// Check Health
	if md.Health != md2.Health {
		return fmt.Errorf("healths not equal, %v and %v", md.Health, md2.Health)
	}
	// Check LastHealthCheckTime
	if md.LastHealthCheckTime != md2.LastHealthCheckTime {
		return fmt.Errorf("lasthealthchecktimes not equal, %v and %v", md.LastHealthCheckTime, md2.LastHealthCheckTime)
	}
	// Check MinRedundancy
	if md.MinRedundancy != md2.MinRedundancy {
		return fmt.Errorf("MinRedundancy not equal, %v and %v", md.MinRedundancy, md2.MinRedundancy)
	}
	// Check ModTimes
	if md.ModTime != md2.ModTime {
		return fmt.Errorf("ModTimes not equal, %v and %v", md.ModTime, md2.ModTime)
	}
	// Check NumFiles
	if md.NumFiles != md2.NumFiles {
		return fmt.Errorf("NumFiles not equal, %v and %v", md.NumFiles, md2.NumFiles)
	}
	// Check NumStuckChunks
	if md.NumStuckChunks != md2.NumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", md.NumStuckChunks, md2.NumStuckChunks)
	}
	// Check NumSubDirs
	if md.NumSubDirs != md2.NumSubDirs {
		return fmt.Errorf("NumSubDirs not equal, %v and %v", md.NumSubDirs, md2.NumSubDirs)
	}
	// Check RootDir
	if md.RootDir != md2.RootDir {
		return fmt.Errorf("rootDirs not equal, %v and %v", md.RootDir, md2.RootDir)
	}
	// Check SiaPath
	if !types.EqualSiaPaths(md.SiaPath, md2.SiaPath) {
		return fmt.Errorf("siapaths not equal, %v and %v", md.SiaPath, md2.SiaPath)
	}
	// Check Size
	if md.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("aggregate sizes not equal, %v and %v", md.AggregateSize, md2.AggregateSize)
	}
	// Check StuckHealth
	if md.StuckHealth != md2.StuckHealth {
		return fmt.Errorf("stuck healths not equal, %v and %v", md.StuckHealth, md2.StuckHealth)
	}

	return nil
}

// newRandSiaPath creates a new SiaPath type with a random path.
func newRandSiaPath() types.SiaPath {
	siaPath, err := types.NewSiaPath(hex.EncodeToString(fastrand.Bytes(4)))
	if err != nil {
		panic(err)
	}
	return siaPath
}

// newTestDir creates a new SiaDir for testing, the test Name should be passed
// in as the rootDir
func newTestDir(rootDir string) (*SiaDir, error) {
	rootPath := filepath.Join(os.TempDir(), "siadirs", rootDir)
	if err := os.RemoveAll(rootPath); err != nil {
		return nil, err
	}
	wal, _ := newTestWAL()
	return New(newRandSiaPath(), rootPath, wal)
}

// newTestWal is a helper method to create a WAL for testing.
func newTestWAL() (*writeaheadlog.WAL, string) {
	// Create the wal.
	walsDir := filepath.Join(os.TempDir(), "wals")
	if err := os.MkdirAll(walsDir, 0700); err != nil {
		panic(err)
	}
	walFilePath := filepath.Join(walsDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walFilePath)
	if err != nil {
		panic(err)
	}
	return wal, walFilePath
}

// TestCreateReadMetadataUpdate tests if an update can be created using createMetadataUpdate
// and if the created update can be read using readMetadataUpdate.
func TestCreateReadMetadataUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sd, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Create metadata update
	update, err := createMetadataUpdate(sd.metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Read metadata update
	data, path, err := readMetadataUpdate(update)
	if err != nil {
		t.Fatal("Failed to read update", err)
	}

	// Check path
	path2 := sd.metadata.SiaPath.SiaDirMetadataSysPath(sd.metadata.RootDir)
	if path != path2 {
		t.Fatalf("Path not correct: expected %v got %v", path2, path)
	}

	// Check data
	var metadata Metadata
	err = json.Unmarshal(data, &metadata)
	if err != nil {
		t.Fatal(err)
	}
	// Check Time separately due to how the time is persisted
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.ModTime.Equal(sd.metadata.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", metadata.ModTime, sd.metadata.ModTime)
	}
	sd.metadata.ModTime = metadata.ModTime
	if err := equalMetadatas(metadata, sd.metadata); err != nil {
		t.Fatal(err)
	}
}

// TestCreateReadDeleteUpdate tests if an update can be created using
// createDeleteUpdate and if the created update can be read using
// readDeleteUpdate.
func TestCreateReadDeleteUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sd, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	update := sd.createDeleteUpdate()
	// Read update
	path := readDeleteUpdate(update)
	// Compare values
	siaDirPath := sd.metadata.SiaPath.DirSysPath(sd.metadata.RootDir)
	if path != siaDirPath {
		t.Error("paths don't match")
	}
}

// TestApplyUpdates tests a variety of functions that are used to apply
// updates.
func TestApplyUpdates(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("TestApplyUpdates", func(t *testing.T) {
		siadir, err := newTestDir(t.Name())
		if err != nil {
			t.Fatal(err)
		}
		testApply(t, siadir, ApplyUpdates)
	})
	t.Run("TestSiaDirApplyUpdates", func(t *testing.T) {
		siadir, err := newTestDir(t.Name())
		if err != nil {
			t.Fatal(err)
		}
		testApply(t, siadir, siadir.applyUpdates)
	})
	t.Run("TestCreateAndApplyTransaction", func(t *testing.T) {
		siadir, err := newTestDir(t.Name())
		if err != nil {
			t.Fatal(err)
		}
		testApply(t, siadir, siadir.createAndApplyTransaction)
	})
}

// testApply tests if a given method applies a set of updates correctly.
func testApply(t *testing.T, siadir *SiaDir, apply func(...writeaheadlog.Update) error) {
	// Create an update to the metadata
	metadata := siadir.metadata
	metadata.Health = 1.0
	update, err := createMetadataUpdate(metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Apply update.
	if err := apply(update); err != nil {
		t.Fatal("Failed to apply update", err)
	}
	// Open file.
	sd, err := LoadSiaDir(metadata.RootDir, metadata.SiaPath, modules.ProdDependencies, siadir.wal)
	if err != nil {
		t.Fatal("Failed to load siadir", err)
	}
	// Check Time separately due to how the time is persisted
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.ModTime.Equal(sd.metadata.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", metadata.ModTime, sd.metadata.ModTime)
	}
	sd.metadata.ModTime = metadata.ModTime
	// Check if correct data was written.
	if err := equalMetadatas(metadata, sd.metadata); err != nil {
		t.Fatal(err)
	}
}

// TestManagedCreateAndApplyTransactions tests if
// managedCreateAndApplyTransactions applies a set of updates correctly.
func TestManagedCreateAndApplyTransactions(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	siadir, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	// Create an update to the metadata
	metadata := siadir.metadata
	metadata.Health = 1.0
	update, err := createMetadataUpdate(metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Apply update.
	if err := managedCreateAndApplyTransaction(siadir.wal, update); err != nil {
		t.Fatal("Failed to apply update", err)
	}
	// Open file.
	sd, err := LoadSiaDir(metadata.RootDir, metadata.SiaPath, modules.ProdDependencies, siadir.wal)
	if err != nil {
		t.Fatal("Failed to load siadir", err)
	}
	// Check Time separately due to how the time is persisted
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.ModTime.Equal(sd.metadata.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", metadata.ModTime, sd.metadata.ModTime)
	}
	sd.metadata.ModTime = metadata.ModTime
	// Check if correct data was written.
	if err := equalMetadatas(metadata, sd.metadata); err != nil {
		t.Fatal(err)
	}
}
