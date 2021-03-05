package siadir

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/persist"
)

// equalMetadatas is a helper that compares two siaDirMetadatas. If using this
// function to check persistence the time fields should be checked in the test
// itself as well and reset due to how time is persisted
func equalMetadatas(md, md2 Metadata) error {
	// Check Aggregate Fields
	if md.AggregateHealth != md2.AggregateHealth {
		return fmt.Errorf("AggregateHealth not equal, %v and %v", md.AggregateHealth, md2.AggregateHealth)
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
	if md.AggregateRemoteHealth != md2.AggregateRemoteHealth {
		return fmt.Errorf("AggregateRemoteHealth not equal, %v and %v", md.AggregateRemoteHealth, md2.AggregateRemoteHealth)
	}
	if md.AggregateRepairSize != md2.AggregateRepairSize {
		return fmt.Errorf("AggregateRepairSize not equal, %v and %v", md.AggregateRepairSize, md2.AggregateRepairSize)
	}
	if md.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("AggregateSize not equal, %v and %v", md.AggregateSize, md2.AggregateSize)
	}
	if md.AggregateStuckHealth != md2.AggregateStuckHealth {
		return fmt.Errorf("AggregateStuckHealth not equal, %v and %v", md.AggregateStuckHealth, md2.AggregateStuckHealth)
	}
	if md.AggregateStuckSize != md2.AggregateStuckSize {
		return fmt.Errorf("AggregateStuckSize not equal, %v and %v", md.AggregateStuckSize, md2.AggregateStuckSize)
	}

	// Aggregate Skynet Fields
	if md.AggregateSkynetFiles != md2.AggregateSkynetFiles {
		return fmt.Errorf("AggregateSkynetFiles not equal, %v and %v", md.AggregateSkynetFiles, md2.AggregateSkynetFiles)
	}
	if md.AggregateSkynetSize != md2.AggregateSkynetSize {
		return fmt.Errorf("AggregateSkynetSize not equal, %v and %v", md.AggregateSkynetSize, md2.AggregateSkynetSize)
	}

	// Check SiaDir Fields
	if md.Health != md2.Health {
		return fmt.Errorf("Healths not equal, %v and %v", md.Health, md2.Health)
	}
	if md.LastHealthCheckTime != md2.LastHealthCheckTime {
		return fmt.Errorf("LastHealthCheckTime not equal, %v and %v", md.LastHealthCheckTime, md2.LastHealthCheckTime)
	}
	if md.MinRedundancy != md2.MinRedundancy {
		return fmt.Errorf("MinRedundancy not equal, %v and %v", md.MinRedundancy, md2.MinRedundancy)
	}
	if md.ModTime != md2.ModTime {
		return fmt.Errorf("ModTime not equal, %v and %v", md.ModTime, md2.ModTime)
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
	if md.RemoteHealth != md2.RemoteHealth {
		return fmt.Errorf("RemoteHealth not equal, %v and %v", md.RemoteHealth, md2.RemoteHealth)
	}
	if md.RepairSize != md2.RepairSize {
		return fmt.Errorf("RepairSize not equal, %v and %v", md.RepairSize, md2.RepairSize)
	}
	if md.Size != md2.Size {
		return fmt.Errorf("Sizes not equal, %v and %v", md.Size, md2.Size)
	}
	if md.StuckHealth != md2.StuckHealth {
		return fmt.Errorf("StuckHealth not equal, %v and %v", md.StuckHealth, md2.StuckHealth)
	}
	if md.StuckSize != md2.StuckSize {
		return fmt.Errorf("StuckSize not equal, %v and %v", md.StuckSize, md2.StuckSize)
	}

	// Skynet Fields
	if md.SkynetFiles != md2.SkynetFiles {
		return fmt.Errorf("SkynetFiles not equal, %v and %v", md.SkynetFiles, md2.SkynetFiles)
	}
	if md.SkynetSize != md2.SkynetSize {
		return fmt.Errorf("SkynetSize not equal, %v and %v", md.SkynetSize, md2.SkynetSize)
	}

	return nil
}

// newSiaDirTestDir creates a test directory for a siadir test
func newSiaDirTestDir(testDir string) (string, error) {
	rootPath := filepath.Join(os.TempDir(), "siadirs", testDir)
	if err := os.RemoveAll(rootPath); err != nil {
		return "", err
	}
	return rootPath, os.MkdirAll(rootPath, persist.DefaultDiskPermissionsTest)
}

// newTestDir creates a new SiaDir for testing, the test Name should be passed
// in as the rootDir
func newTestDir(rootDir string) (*SiaDir, error) {
	rootPath, err := newSiaDirTestDir(rootDir)
	if err != nil {
		return nil, err
	}
	return New(modules.RandomSiaPath().SiaDirSysPath(rootPath), rootPath, modules.DefaultDirPerm)
}

// TestCreateDirMetadataAll probes the case of a potential infinite loop in
// createDirMetadataAll
func TestCreateDirMetadataAll(t *testing.T) {
	// Ignoring errors, only checking that the functions return
	createDirMetadataAll("path", "", persist.DefaultDiskPermissionsTest)
	createDirMetadataAll("path", ".", persist.DefaultDiskPermissionsTest)
	createDirMetadataAll("path", "/", persist.DefaultDiskPermissionsTest)
}
