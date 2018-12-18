package siadir

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
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
	health, stuckHealth, lastCheck := siaDir.Health()
	if health != DefaultDirHealth {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealthCheckTime not initialized")
	}
	// Check that the StuckHealth was initialized properly
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("SiaDir stuck health not set properly: got %v expected %v", stuckHealth, DefaultDirHealth)
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
	health, stuckHealth, lastCheck = subDir.Health()
	if health != DefaultDirHealth {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealthCheckTime not initialized")
	}
	// Check that the StuckHealth was initialized properly
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("SiaDir stuck health not set properly: got %v expected %v", stuckHealth, DefaultDirHealth)
	}
	// Check that the SiaPath was initialized properly
	if subDir.SiaPath() != siaPathDir {
		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", subDir.SiaPath(), siaPathDir)
	}

	// Check Root Directory
	//
	// Get SiaDir
	rootSiaDir, err := LoadSiaDir(rootDir, "", modules.ProdDependencies, wal)
	// Check that the Health was initialized properly
	health, stuckHealth, lastCheck = rootSiaDir.Health()
	if health != DefaultDirHealth {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealthCheckTime not initialized")
	}
	// Check that the StuckHealth was initialized properly
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("SiaDir stuck health not set properly: got %v expected %v", stuckHealth, DefaultDirHealth)
	}
	// Check that the SiaPath was initialized properly
	if rootSiaDir.SiaPath() != "" {
		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", rootSiaDir.SiaPath(), "")
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

// Test Update Health
func TestUpdateSiaDirHealth(t *testing.T) {
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
	health, stuckHealth, lastCheck := siaDir.Health()
	if health != DefaultDirHealth {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, DefaultDirHealth)
	}
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("SiaDir stuckHealth not set properly: got %v expected %v", stuckHealth, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealthCheckTime not initialized")
	}
	siaDir, err = LoadSiaDir(rootDir, siaPath, modules.ProdDependencies, wal)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, lastCheck = siaDir.Health()
	if health != DefaultDirHealth {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, DefaultDirHealth)
	}
	if stuckHealth != DefaultDirHealth {
		t.Fatalf("SiaDir stuckHealth not set properly: got %v expected %v", stuckHealth, DefaultDirHealth)
	}
	if lastCheck.IsZero() {
		t.Fatal("lastHealthCheckTime not initialized")
	}

	// Set the health
	checkTime := time.Now()
	err = siaDir.UpdateHealth(4, 2, checkTime)
	if err != nil {
		t.Fatal(err)
	}

	// Check Health was updated properly in memory and on disk
	health, stuckHealth, lastCheck = siaDir.Health()
	if health != 4 {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, 4)
	}
	if stuckHealth != 2 {
		t.Fatalf("SiaDir stuckHealth not set properly: got %v expected %v", stuckHealth, 2)
	}
	if !lastCheck.Equal(checkTime) {
		t.Fatalf("lastHealthCheckTime not save correctly, expected %v got %v", checkTime, lastCheck)
	}
	siaDir, err = LoadSiaDir(rootDir, siaPath, modules.ProdDependencies, wal)
	if err != nil {
		t.Fatal(err)
	}
	health, stuckHealth, lastCheck = siaDir.Health()
	if health != 4 {
		t.Fatalf("SiaDir health not set properly: got %v expected %v", health, 4)
	}
	if stuckHealth != 2 {
		t.Fatalf("SiaDir stuckHealth not set properly: got %v expected %v", stuckHealth, 2)
	}
	if !lastCheck.Equal(checkTime) {
		t.Fatalf("lastHealthCheckTime not save correctly, expected %v got %v", checkTime, lastCheck)
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
