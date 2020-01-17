package filesystem

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/errors"
)

// checkMetadataInit is a helper that verifies that the metadata was initialized
// properly
func checkMetadataInit(md siadir.Metadata) error {
	// Check Aggregate Fields
	if md.AggregateHealth != siadir.DefaultDirHealth {
		return fmt.Errorf("SiaDir AggregateHealth not set properly: got %v expected %v", md.AggregateHealth, siadir.DefaultDirHealth)
	}
	if !md.AggregateLastHealthCheckTime.IsZero() {
		return fmt.Errorf("AggregateLastHealthCheckTime should be zero but was %v", md.AggregateLastHealthCheckTime)
	}
	if md.AggregateMinRedundancy != siadir.DefaultDirRedundancy {
		return fmt.Errorf("SiaDir AggregateMinRedundancy not set properly: got %v expected %v", md.AggregateMinRedundancy, siadir.DefaultDirRedundancy)
	}
	if md.AggregateModTime.IsZero() {
		return errors.New("AggregateModTime not initialized")
	}
	if md.AggregateNumFiles != 0 {
		return fmt.Errorf("SiaDir AggregateNumFiles not set properly: got %v expected 0", md.AggregateNumFiles)
	}
	if md.AggregateNumStuckChunks != 0 {
		return fmt.Errorf("SiaDir AggregateNumStuckChunks not initialized properly, expected 0, got %v", md.AggregateNumStuckChunks)
	}
	if md.AggregateNumSubDirs != 0 {
		return fmt.Errorf("SiaDir AggregateNumSubDirs not initialized properly, expected 0, got %v", md.AggregateNumSubDirs)
	}
	if md.AggregateStuckHealth != siadir.DefaultDirHealth {
		return fmt.Errorf("SiaDir AggregateStuckHealth not set properly: got %v expected %v", md.AggregateStuckHealth, siadir.DefaultDirHealth)
	}
	if md.AggregateSize != 0 {
		return fmt.Errorf("SiaDir AggregateSize not set properly: got %v expected 0", md.AggregateSize)
	}

	// Check SiaDir Fields
	if md.Health != siadir.DefaultDirHealth {
		return fmt.Errorf("SiaDir Health not set properly: got %v expected %v", md.Health, siadir.DefaultDirHealth)
	}
	if !md.LastHealthCheckTime.IsZero() {
		return fmt.Errorf("LastHealthCheckTime should be zero but was %v", md.LastHealthCheckTime)
	}
	if md.MinRedundancy != siadir.DefaultDirRedundancy {
		return fmt.Errorf("SiaDir MinRedundancy not set properly: got %v expected %v", md.MinRedundancy, siadir.DefaultDirRedundancy)
	}
	if md.ModTime.IsZero() {
		return errors.New("ModTime not initialized")
	}
	if md.NumFiles != 0 {
		return fmt.Errorf("SiaDir NumFiles not initialized properly, expected 0, got %v", md.NumFiles)
	}
	if md.NumStuckChunks != 0 {
		return fmt.Errorf("SiaDir NumStuckChunks not initialized properly, expected 0, got %v", md.NumStuckChunks)
	}
	if md.NumSubDirs != 0 {
		return fmt.Errorf("SiaDir NumSubDirs not initialized properly, expected 0, got %v", md.NumSubDirs)
	}
	if md.StuckHealth != siadir.DefaultDirHealth {
		return fmt.Errorf("SiaDir stuck health not set properly: got %v expected %v", md.StuckHealth, siadir.DefaultDirHealth)
	}
	if md.Size != 0 {
		return fmt.Errorf("SiaDir Size not set properly: got %v expected 0", md.Size)
	}
	return nil
}

// equalMetadatas is a helper that compares two siaDirMetadatas. If using this
// function to check persistence the time fields should be checked in the test
// itself as well and reset due to how time is persisted
func equalMetadatas(md, md2 siadir.Metadata) error {
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

// TestHealthPercentage checks the values returned from HealthPercentage
func TestHealthPercentage(t *testing.T) {
	var tests = []struct {
		health           float64
		healthPercentage float64
	}{
		{1.5, 0},
		{1.25, 0},
		{1.0, 25},
		{0.75, 50},
		{0.5, 75},
		{0.25, 100},
		{0, 100},
	}
	for _, test := range tests {
		hp := modules.HealthPercentage(test.health)
		if hp != test.healthPercentage {
			t.Fatalf("Expect %v got %v", test.healthPercentage, hp)
		}
	}
}

// TestUpdateSiaDirSetMetadata probes the UpdateMetadata method of the SiaDirSet
func TestUpdateSiaDirSetMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Prepare a filesystem with a dir.
	root := filepath.Join(testDir(t.Name()), "fs-root")
	fs := newTestFileSystem(root)
	sp := newSiaPath("path/to/dir")
	err := fs.NewSiaDir(sp, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := fs.OpenSiaDir(sp)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm metadata is set properly
	md, err := entry.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Update the metadata of the entry
	checkTime := time.Now()
	metadataUpdate := md
	// Aggregate fields
	metadataUpdate.AggregateHealth = 7
	metadataUpdate.AggregateLastHealthCheckTime = checkTime
	metadataUpdate.AggregateMinRedundancy = 2.2
	metadataUpdate.AggregateModTime = checkTime
	metadataUpdate.AggregateNumFiles = 11
	metadataUpdate.AggregateNumStuckChunks = 15
	metadataUpdate.AggregateNumSubDirs = 5
	metadataUpdate.AggregateSize = 2432
	metadataUpdate.AggregateStuckHealth = 5
	// SiaDir fields
	metadataUpdate.Health = 4
	metadataUpdate.LastHealthCheckTime = checkTime
	metadataUpdate.MinRedundancy = 2
	metadataUpdate.ModTime = checkTime
	metadataUpdate.NumFiles = 5
	metadataUpdate.NumStuckChunks = 6
	metadataUpdate.NumSubDirs = 4
	metadataUpdate.Size = 223
	metadataUpdate.StuckHealth = 2

	err = fs.UpdateDirMetadata(sp, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Check if the metadata was updated properly in memory and on disk
	md, err = entry.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	err = equalMetadatas(md, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}
}
