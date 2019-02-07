package renter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/errors"
)

// TestRenterCreateDirectories checks that the renter properly created metadata files
// for direcotries
func TestRenterCreateDirectories(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Test creating directory
	err = rt.renter.CreateDir("foo/bar/baz")
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that direcotry metadata files were created in all directories
	if err := rt.checkDirInitialized(""); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized("foo"); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized("foo/bar"); err != nil {
		t.Fatal(err)
	}
	if err := rt.checkDirInitialized("foo/bar/baz"); err != nil {
		t.Fatal(err)
	}
}

// checkDirInitialized is a helper function that checks that the directory was
// initialized correctly and the metadata file exist and contain the correct
// information
func (rt *renterTester) checkDirInitialized(siaPath string) error {
	fullpath := filepath.Join(rt.renter.staticFilesDir, siaPath, siadir.SiaDirExtension)
	if _, err := os.Stat(fullpath); err != nil {
		return err
	}
	siaDir, err := rt.renter.staticDirSet.Open(siaPath)
	if err != nil {
		return fmt.Errorf("unable to load directory %v metadata: %v", siaPath, err)
	}
	defer siaDir.Close()

	// Check that health is default value
	health := siaDir.Health()
	defaultHealth := siadir.SiaDirHealth{
		Health:      siadir.DefaultDirHealth,
		StuckHealth: siadir.DefaultDirHealth,
	}
	if err = equalHealthsAndChunks(health, defaultHealth); err != nil {
		return err
	}
	// Check that the LastHealthCheckTime is not zero
	if health.LastHealthCheckTime.IsZero() {
		return errors.New("LastHealthCheckTime was not initialized")
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory
	err = rt.renter.CreateDir("foo/")
	if err != nil {
		t.Fatal(err)
	}

	// Check that DirInfo returns the same information as stored in the metadata
	fooDirInfo, err := rt.renter.DirInfo("foo")
	if err != nil {
		t.Fatal(err)
	}
	rootDirInfo, err := rt.renter.DirInfo("")
	if err != nil {
		t.Fatal(err)
	}
	fooEntry, err := rt.renter.staticDirSet.Open("foo")
	if err != nil {
		t.Fatal(err)
	}
	rootEntry, err := rt.renter.staticDirSet.Open("")
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory
	err = rt.renter.CreateDir("foo/")
	if err != nil {
		t.Fatal(err)
	}

	// Upload a file
	_, err = rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}

	// Confirm that DirList returns 1 FileInfo and 2 DirectoryInfos
	directories, files, err := rt.renter.DirList("")
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
	rootDir, err := rt.renter.staticDirSet.Open("")
	if err != nil {
		t.Fatal(err)
	}
	fooDir, err := rt.renter.staticDirSet.Open("foo")
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
	health := siaDir.Health()
	if di.SiaPath != siaDir.SiaPath() {
		return fmt.Errorf("SiaPaths not equal %v and %v", di.SiaPath, siaDir.SiaPath())
	}
	if di.Health != health.StuckHealth {
		return fmt.Errorf("Healths not equal %v and %v", di.SiaPath, health.StuckHealth)
	}
	if di.LastHealthCheckTime != health.LastHealthCheckTime {
		return fmt.Errorf("LastHealthCheckTimes not equal %v and %v", di.LastHealthCheckTime, health.LastHealthCheckTime)
	}
	return nil
}
