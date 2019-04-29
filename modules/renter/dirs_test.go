package renter

import (
	"fmt"
	"os"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// TestRenterCreateDirectories checks that the renter properly created metadata files
// for direcotries
func TestRenterCreateDirectories(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Test creating directory
	siaPath, err := modules.NewSiaPath("foo/bar/baz")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.CreateDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that directory metadata files were created in all directories
	if err := rt.checkDirInitialized(modules.RootSiaPath()); err != nil {
		t.Fatal(err)
	}
	siaPath, err = modules.NewSiaPath("foo")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized(siaPath); err != nil {
		t.Fatal(err)
	}
	siaPath, err = modules.NewSiaPath("foo/bar")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized(siaPath); err != nil {
		t.Fatal(err)
	}
	siaPath, err = modules.NewSiaPath("foo/bar/baz")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized(siaPath); err != nil {
		t.Fatal(err)
	}
}

// checkDirInitialized is a helper function that checks that the directory was
// initialized correctly and the metadata file exist and contain the correct
// information
func (rt *renterTester) checkDirInitialized(siaPath modules.SiaPath) error {
	fullpath := siaPath.SiaDirMetadataSysPath(rt.renter.staticFilesDir)
	if _, err := os.Stat(fullpath); err != nil {
		return err
	}
	siaDir, err := rt.renter.staticDirSet.Open(siaPath)
	if err != nil {
		return fmt.Errorf("unable to load directory %v metadata: %v", siaPath, err)
	}
	defer siaDir.Close()

	// Check that metadata is default value
	metadata := siaDir.Metadata()
	if metadata.Health != siadir.DefaultDirHealth {
		return fmt.Errorf("Health not initialized properly: have %v expected %v", metadata.Health, siadir.DefaultDirHealth)
	}
	if metadata.StuckHealth != siadir.DefaultDirHealth {
		return fmt.Errorf("StuckHealth not initialized properly: have %v expected %v", metadata.StuckHealth, siadir.DefaultDirHealth)
	}
	if !metadata.LastHealthCheckTime.IsZero() {
		return fmt.Errorf("LastHealthCheckTime should be a zero timestamp: %v", metadata.LastHealthCheckTime)
	}
	if metadata.ModTime.IsZero() {
		return fmt.Errorf("ModTime not initialized: %v", metadata.ModTime)
	}
	// Check that the SiaPath was initialized properly
	if siaDir.SiaPath() != siaPath {
		return fmt.Errorf("Expected siapath to be %v, got %v", siaPath, siaDir.SiaPath())
	}
	return nil
}

// TestDirInfo probes the DirInfo method
func TestDirInfo(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory
	siaPath, err := modules.NewSiaPath("foo/")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.CreateDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Check that DirInfo returns the same information as stored in the metadata
	fooDirInfo, err := rt.renter.DirInfo(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	rootDirInfo, err := rt.renter.DirInfo(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	fooEntry, err := rt.renter.staticDirSet.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	rootEntry, err := rt.renter.staticDirSet.Open(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	err = compareDirectoryInfoAndMetadata(fooDirInfo, fooEntry)
	if err != nil {
		t.Fatal(err)
	}
	err = compareDirectoryInfoAndMetadata(rootDirInfo, rootEntry)
	if err != nil {
		t.Fatal(err)
	}
}

// TestRenterListDirectory verifies that the renter properly lists the contents
// of a directory
func TestRenterListDirectory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory
	siaPath, err := modules.NewSiaPath("foo/")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.CreateDir(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Upload a file
	_, err = rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that DirList returns 1 FileInfo and 2 DirectoryInfos
	directories, files, err := rt.renter.DirList(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(directories) != 2 {
		t.Fatal("Expected 2 DirectoryInfos but got", len(directories))
	}
	if len(files) != 1 {
		t.Fatal("Expected 1 FileInfos but got", len(files))
	}

	// Verify that the directory information matches the on disk information
	rootDir, err := rt.renter.staticDirSet.Open(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	fooDir, err := rt.renter.staticDirSet.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = compareDirectoryInfoAndMetadata(directories[0], rootDir); err != nil {
		t.Fatal(err)
	}
	if err = compareDirectoryInfoAndMetadata(directories[1], fooDir); err != nil {
		t.Fatal(err)
	}
}

// compareDirectoryInfoAndMetadata is a helper that compares the information in
// a DirectoryInfo struct and a SiaDirSetEntry struct
func compareDirectoryInfoAndMetadata(di modules.DirectoryInfo, siaDir *siadir.SiaDirSetEntry) error {
	md := siaDir.Metadata()

	// Check AggregateNumFiles
	if md.AggregateNumFiles != di.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md.AggregateNumFiles, di.AggregateNumFiles)
	}
	// Check Size
	if md.AggregateSize != di.AggregateSize {
		return fmt.Errorf("aggregate sizes not equal, %v and %v", md.AggregateSize, di.AggregateSize)
	}
	// Check Health
	if md.Health != di.Health {
		return fmt.Errorf("healths not equal, %v and %v", md.Health, di.Health)
	}
	// Check LastHealthCheckTimes
	if di.LastHealthCheckTime != md.LastHealthCheckTime {
		return fmt.Errorf("LastHealthCheckTimes not equal %v and %v", di.LastHealthCheckTime, md.LastHealthCheckTime)
	}
	// Check MinRedundancy
	if md.MinRedundancy != di.MinRedundancy {
		return fmt.Errorf("MinRedundancy not equal, %v and %v", md.MinRedundancy, di.MinRedundancy)
	}
	// Check Mod Times
	if di.MostRecentModTime != md.ModTime {
		return fmt.Errorf("ModTimes not equal %v and %v", di.MostRecentModTime, md.ModTime)
	}
	// Check NumFiles
	if md.NumFiles != di.NumFiles {
		return fmt.Errorf("NumFiles not equal, %v and %v", md.NumFiles, di.NumFiles)
	}
	// Check NumStuckChunks
	if md.NumStuckChunks != di.AggregateNumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", md.NumStuckChunks, di.AggregateNumStuckChunks)
	}
	// Check NumSubDirs
	if md.NumSubDirs != di.NumSubDirs {
		return fmt.Errorf("NumSubDirs not equal, %v and %v", md.NumSubDirs, di.NumSubDirs)
	}
	// Check SiaPath
	if !siaDir.SiaPath().Equals(di.SiaPath) {
		return fmt.Errorf("siapaths not equal, %v and %v", siaDir.SiaPath(), di.SiaPath)
	}
	// Check StuckHealth
	if md.StuckHealth != di.StuckHealth {
		return fmt.Errorf("stuck healths not equal, %v and %v", md.StuckHealth, di.StuckHealth)
	}
	return nil
}
