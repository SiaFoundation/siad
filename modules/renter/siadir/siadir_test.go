package siadir

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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
	siaDir, err := New(siaPath, rootDir)
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
	// Get SiaDir
	subDir, err := LoadSiaDir(rootDir, siaPathDir)
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
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(filepath.Join(rootDir, siaPathDir))
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(rootDir, siaPathDir, SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}

	// Check Root Directory
	//
	// Get SiaDir
	rootSiaDir, err := LoadSiaDir(rootDir, "")
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
	siaDir, err := New(siaPath, rootDir)
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
	siaDir, err = LoadSiaDir(rootDir, siaPath)
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
	err = siaDir.UpdateHealth(4, 2, checkTime, filepath.Join(rootDir, siaPath))
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
	siaDir, err = LoadSiaDir(rootDir, siaPath)
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
