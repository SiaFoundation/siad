package siadir

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// newRootDir creates a root directory for the test and removes old test files
func newRootDir(t *testing.T) (string, error) {
	dir := filepath.Join(os.TempDir(), "siadirs", t.Name())
	err := os.RemoveAll(dir)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// TestNewSiaDir tests that siadirs are created on disk properly. It uses
// LoadSiaDir to read the metadata from disk
func TestNewSiaDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create New SiaDir that is two levels deep
	rootDir, err := newRootDir(t)
	if err != nil {
		t.Fatal(err)
	}
	siaPathDir := "TestDir"
	siaPathSubDir := "SubDir"
	siaPath := filepath.Join(siaPathDir, siaPathSubDir)
	wal, _ := newTestWAL()
	siaDir, err := New(siaPath, rootDir, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Check Sub Dir
	//
	// Check that the Health was initialized properly
	health := siaDir.Health()
	if err = checkHealthInit(health); err != nil {
		t.Fatal(err)
	}
	// Check that the SiaPath was initialized properly
	if siaDir.SiaPath() != siaPath {
		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", siaDir.SiaPath(), siaPath)
	}
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(filepath.Join(rootDir, siaPath))
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(rootDir, siaPath, SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}

	// Check Top Directory
	//
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(filepath.Join(rootDir, siaPathDir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(rootDir, siaPathDir, SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}
	// Get SiaDir
	subDir, err := LoadSiaDir(rootDir, siaPathDir, modules.ProdDependencies, wal)
	// Check that the Health was initialized properly
	health = subDir.Health()
	if err = checkHealthInit(health); err != nil {
		t.Fatal(err)
	}
	// Check that the SiaPath was initialized properly
	if siaDir.SiaPath() != siaPath {
		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", siaDir.SiaPath(), siaPath)
	}

	// Check Root Directory
	//
	// Get SiaDir
	rootSiaDir, err := LoadSiaDir(rootDir, "", modules.ProdDependencies, wal)
	// Check that the Health was initialized properly
	health = rootSiaDir.Health()
	if err = checkHealthInit(health); err != nil {
		t.Fatal(err)
	}
	// Check that the SiaPath was initialized properly
	if siaDir.SiaPath() != siaPath {
		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", siaDir.SiaPath(), siaPath)
	}
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(rootDir, SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}
}

// checkHealthInit is a helper that verifies that the health was initialized
// properly
func checkHealthInit(health SiaDirHealth) error {
	if health.Health != DefaultDirHealth {
		return fmt.Errorf("SiaDir health not set properly: got %v expected %v", health.Health, DefaultDirHealth)
	}
	if health.LastHealthCheckTime.IsZero() {
		return errors.New("lastHealthCheckTime not initialized")
	}
	if health.StuckHealth != DefaultDirHealth {
		return fmt.Errorf("SiaDir stuck health not set properly: got %v expected %v", health.StuckHealth, DefaultDirHealth)
	}
	if health.NumStuckChunks != 0 {
		return fmt.Errorf("SiaDir NumStuckChunks not initialized properly, expected 0, got %v", health.NumStuckChunks)
	}
	return nil
}

// Test Update Health
func TestUpdateSiaDirHealth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create new siaDir
	rootDir, err := newRootDir(t)
	if err != nil {
		t.Fatal(err)
	}
	siaPath := "TestDir"
	wal, _ := newTestWAL()
	siaDir, err := New(siaPath, rootDir, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Check Health was initialized properly in memory and on disk
	health := siaDir.Health()
	if err = checkHealthInit(health); err != nil {
		t.Fatal(err)
	}
	siaDir, err = LoadSiaDir(rootDir, siaPath, modules.ProdDependencies, wal)
	if err != nil {
		t.Fatal(err)
	}
	health = siaDir.Health()
	if err = checkHealthInit(health); err != nil {
		t.Fatal(err)
	}

	// Set the health
	checkTime := time.Now()
	healthUpdate := SiaDirHealth{
		Health:              4,
		StuckHealth:         2,
		LastHealthCheckTime: checkTime,
		NumStuckChunks:      5,
	}
	err = siaDir.UpdateHealth(healthUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Check Health was updated properly in memory and on disk
	health = siaDir.Health()
	if !reflect.DeepEqual(health, healthUpdate) {
		t.Log("Health", health)
		t.Log("Health Update", healthUpdate)
		t.Fatal("health not updated correctly")
	}
	siaDir, err = LoadSiaDir(rootDir, siaPath, modules.ProdDependencies, wal)
	if err != nil {
		t.Fatal(err)
	}
	health = siaDir.Health()
	// Check Time separately due to how the time is persisted
	if !health.LastHealthCheckTime.Equal(healthUpdate.LastHealthCheckTime) {
		t.Fatalf("Times not equal, got %v expected %v", health.LastHealthCheckTime, healthUpdate.LastHealthCheckTime)
	}
	healthUpdate.LastHealthCheckTime = health.LastHealthCheckTime
	// Check the rest of the metadata
	if !reflect.DeepEqual(health, healthUpdate) {
		t.Log("Health", health)
		t.Log("Health Update", healthUpdate)
		t.Fatal("health not updated correctly")
	}
}

// TestDelete tests if deleting a siadir removes the siadir from disk and sets
// the deleted flag correctly.
func TestDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaDir
	entry, _, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	// Delete siadir.
	if err := entry.Delete(); err != nil {
		t.Fatal("Failed to delete siadir", err)
	}
	// Check if siadir was deleted and if deleted flag was set.
	if !entry.Deleted() {
		t.Fatal("Deleted flag was not set correctly")
	}
	siaDirPath := filepath.Join(entry.staticMetadata.RootDir, entry.staticMetadata.SiaPath)
	if _, err := os.Open(siaDirPath); !os.IsNotExist(err) {
		t.Fatal("Expected a siadir doesn't exist error but got", err)
	}
}
