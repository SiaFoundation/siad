package siadir

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// equalMetadatas is a helper that compares two siaDirMetadatas. If using this
// function to check persistence the time fields should be checked in the test
// itself as well and reset due to how time is persisted
func equalMetadatas(md, md2 Metadata) error {
	// Check Aggregate Fields
	if md.AggregateHealth != md2.AggregateHealth {
		return fmt.Errorf("AggregateHealths not equal, %v and %v", md.AggregateHealth, md2.AggregateHealth)
	}
	if md.AggregateLastHealthCheckTime != md2.AggregateLastHealthCheckTime {
		return fmt.Errorf("AggregateLastHealthCheckTimes not equal, %v and %v", md.AggregateLastHealthCheckTime, md2.AggregateLastHealthCheckTime)
	}
	if md.AggregateMinRedundancy != md2.AggregateMinRedundancy {
		return fmt.Errorf("AggregateMinRedundancy not equal, %v and %v", md.AggregateMinRedundancy, md2.AggregateMinRedundancy)
	}
	if md.AggregateModTime != md2.AggregateModTime {
		return fmt.Errorf("AggregateModTimes not equal, %v and %v", md.AggregateModTime, md2.AggregateModTime)
	}
	if md.AggregateNumFiles != md2.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md.AggregateNumFiles, md2.AggregateNumFiles)
	}
	if md.AggregateNumStuckChunks != md2.AggregateNumStuckChunks {
		return fmt.Errorf("AggregateNumStuckChunks not equal, %v and %v", md.AggregateNumStuckChunks, md2.AggregateNumStuckChunks)
	}
	if md.AggregateNumSubDirs != md2.AggregateNumSubDirs {
		return fmt.Errorf("AggregateNumSubDirs not equal, %v and %v", md.AggregateNumSubDirs, md2.AggregateNumSubDirs)
	}
	if md.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("AggregateSizes not equal, %v and %v", md.AggregateSize, md2.AggregateSize)
	}
	if md.AggregateStuckHealth != md2.AggregateStuckHealth {
		return fmt.Errorf("AggregateStuckHealths not equal, %v and %v", md.AggregateStuckHealth, md2.AggregateStuckHealth)
	}

	// Check SiaDir Fields
	if md.Health != md2.Health {
		return fmt.Errorf("Healths not equal, %v and %v", md.Health, md2.Health)
	}
	if md.LastHealthCheckTime != md2.LastHealthCheckTime {
		return fmt.Errorf("lasthealthchecktimes not equal, %v and %v", md.LastHealthCheckTime, md2.LastHealthCheckTime)
	}
	if md.MinRedundancy != md2.MinRedundancy {
		return fmt.Errorf("MinRedundancy not equal, %v and %v", md.MinRedundancy, md2.MinRedundancy)
	}
	if md.ModTime != md2.ModTime {
		return fmt.Errorf("ModTimes not equal, %v and %v", md.ModTime, md2.ModTime)
	}
	if md.NumFiles != md2.NumFiles {
		return fmt.Errorf("NumFiles not equal, %v and %v", md.NumFiles, md2.NumFiles)
	}
	if md.NumStuckChunks != md2.NumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", md.NumStuckChunks, md2.NumStuckChunks)
	}
	if md.NumSubDirs != md2.NumSubDirs {
		return fmt.Errorf("NumSubDirs not equal, %v and %v", md.NumSubDirs, md2.NumSubDirs)
	}
	if md.Size != md2.Size {
		return fmt.Errorf("Sizes not equal, %v and %v", md.Size, md2.Size)
	}
	if md.StuckHealth != md2.StuckHealth {
		return fmt.Errorf("StuckHealths not equal, %v and %v", md.StuckHealth, md2.StuckHealth)
	}

	return nil
}

// newTestDir creates a new SiaDir for testing, the test Name should be passed
// in as the rootDir
func newTestDir(rootDir string) (*SiaDir, error) {
	rootPath := filepath.Join(os.TempDir(), "siadirs", rootDir)
	if err := os.RemoveAll(rootPath); err != nil {
		return nil, err
	}
	wal, _ := newTestWAL()
	return New(modules.RandomSiaPath().SiaDirSysPath(rootPath), rootPath, modules.DefaultDirPerm, wal)
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

// TestIsSiaDirUpdate tests the IsSiaDirUpdate method.
func TestIsSiaDirUpdate(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	sd, err := newTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	metadataUpdate, err := createMetadataUpdate(sd.Path(), Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	deleteUpdate := sd.createDeleteUpdate()
	emptyUpdate := writeaheadlog.Update{}

	if !IsSiaDirUpdate(metadataUpdate) {
		t.Error("metadataUpdate should be a SiaDirUpdate but wasn't")
	}
	if !IsSiaDirUpdate(deleteUpdate) {
		t.Error("deleteUpdate should be a SiaDirUpdate but wasn't")
	}
	if IsSiaDirUpdate(emptyUpdate) {
		t.Error("emptyUpdate shouldn't be a SiaDirUpdate but was one")
	}
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
	path := filepath.Join(sd.path, modules.SiaDirExtension)
	update, err := createMetadataUpdate(path, sd.metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Read metadata update
	data, path, err := readMetadataUpdate(update)
	if err != nil {
		t.Fatal("Failed to read update", err)
	}

	// Check path
	path2 := filepath.Join(sd.path, modules.SiaDirExtension)
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
	if !metadata.AggregateLastHealthCheckTime.Equal(sd.metadata.AggregateLastHealthCheckTime) {
		t.Fatalf("AggregateLastHealthCheckTimes not equal, got %v expected %v", metadata.AggregateLastHealthCheckTime, sd.metadata.AggregateLastHealthCheckTime)
	}
	sd.metadata.AggregateLastHealthCheckTime = metadata.AggregateLastHealthCheckTime
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.AggregateModTime.Equal(sd.metadata.AggregateModTime) {
		t.Fatalf("AggregateModTimes not equal, got %v expected %v", metadata.AggregateModTime, sd.metadata.AggregateModTime)
	}
	sd.metadata.AggregateModTime = metadata.AggregateModTime
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
	siaDirPath := sd.path
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
	path := filepath.Join(siadir.path, modules.SiaDirExtension)
	update, err := createMetadataUpdate(path, metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Apply update.
	if err := apply(update); err != nil {
		t.Fatal("Failed to apply update", err)
	}
	// Open file.
	sd, err := LoadSiaDir(siadir.path, modules.ProdDependencies, siadir.wal)
	if err != nil {
		t.Fatal("Failed to load siadir", err)
	}
	// Check Time separately due to how the time is persisted
	if !metadata.AggregateLastHealthCheckTime.Equal(sd.metadata.AggregateLastHealthCheckTime) {
		t.Fatalf("AggregateLastHealthCheckTimes not equal, got %v expected %v", metadata.AggregateLastHealthCheckTime, sd.metadata.AggregateLastHealthCheckTime)
	}
	sd.metadata.AggregateLastHealthCheckTime = metadata.AggregateLastHealthCheckTime
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.AggregateModTime.Equal(sd.metadata.AggregateModTime) {
		t.Fatalf("AggregateModTimes not equal, got %v expected %v", metadata.AggregateModTime, sd.metadata.AggregateModTime)
	}
	sd.metadata.AggregateModTime = metadata.AggregateModTime
	if !metadata.ModTime.Equal(sd.metadata.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", metadata.ModTime, sd.metadata.ModTime)
	}
	sd.metadata.ModTime = metadata.ModTime
	// Check if correct data was written.
	if err := equalMetadatas(metadata, sd.metadata); err != nil {
		t.Fatal(err)
	}
}

// TestCreateAndApplyTransactions tests if CreateAndApplyTransactions applies a
// set of updates correctly.
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
	path := filepath.Join(siadir.path, modules.SiaDirExtension)
	update, err := createMetadataUpdate(path, metadata)
	if err != nil {
		t.Fatal(err)
	}

	// Apply update.
	if err := CreateAndApplyTransaction(siadir.wal, update); err != nil {
		t.Fatal("Failed to apply update", err)
	}
	// Open file.
	sd, err := LoadSiaDir(siadir.path, modules.ProdDependencies, siadir.wal)
	if err != nil {
		t.Fatal("Failed to load siadir", err)
	}
	// Check Time separately due to how the time is persisted
	if !metadata.AggregateLastHealthCheckTime.Equal(sd.metadata.AggregateLastHealthCheckTime) {
		t.Fatalf("AggregateLastHealthCheckTimes not equal, got %v expected %v", metadata.AggregateLastHealthCheckTime, sd.metadata.AggregateLastHealthCheckTime)
	}
	sd.metadata.AggregateLastHealthCheckTime = metadata.AggregateLastHealthCheckTime
	if !metadata.LastHealthCheckTime.Equal(sd.metadata.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", metadata.LastHealthCheckTime, sd.metadata.LastHealthCheckTime)
	}
	sd.metadata.LastHealthCheckTime = metadata.LastHealthCheckTime
	if !metadata.AggregateModTime.Equal(sd.metadata.AggregateModTime) {
		t.Fatalf("AggregateModTimes not equal, got %v expected %v", metadata.AggregateModTime, sd.metadata.AggregateModTime)
	}
	sd.metadata.AggregateModTime = metadata.AggregateModTime
	if !metadata.ModTime.Equal(sd.metadata.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", metadata.ModTime, sd.metadata.ModTime)
	}
	sd.metadata.ModTime = metadata.ModTime
	// Check if correct data was written.
	if err := equalMetadatas(metadata, sd.metadata); err != nil {
		t.Fatal(err)
	}
}
