package renter

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/errors"
)

// TestRenterDirectories checks that the renter properly created metadata files
// for direcotries
func TestRenterDirectories(t *testing.T) {
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
	fullpath := filepath.Join(rt.renter.filesDir, siaPath, siadir.SiaDirExtension)
	if _, err := os.Stat(fullpath); err != nil {
		return err
	}
	siaDir, err := rt.renter.staticDirSet.Open(siaPath)
	if err != nil {
		return fmt.Errorf("unable to load directory %v metadata: %v", siaPath, err)
	}
	defer siaDir.Close()

	// Check that health is default value
	health, stuckHealth, lastCheck := siaDir.Health()
	if health != siadir.DefaultDirHealth {
		return fmt.Errorf("Expected Health to be %v, but instead was %v", siadir.DefaultDirHealth, health)
	}
	if lastCheck.IsZero() {
		return errors.New("LastHealthCheckTime was not initialized")
	}
	if stuckHealth != siadir.DefaultDirHealth {
		return fmt.Errorf("Expected Stuck Health to be %v, but instead was %v", siadir.DefaultDirHealth, stuckHealth)
	}
	// Check that the SiaPath was initialized properly
	if siaDir.SiaPath() != siaPath {
		return fmt.Errorf("Expected siapath to be %v, got %v", siaPath, siaDir.SiaPath())
	}
	return nil
}
