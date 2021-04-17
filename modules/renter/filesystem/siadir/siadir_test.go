package siadir

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/persist"
)

// TestSiaDir probes the SiaDir subsystem
func TestSiaDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	t.Run("Basic", testSiaDirBasic)
	t.Run("Delete", testSiaDirDelete)
	t.Run("UpdatedMetadata", testUpdateMetadata)
}

// testSiaDirBasic tests the basic functionality of the siadir
func testSiaDirBasic(t *testing.T) {
	// Initialize the test directory
	testDir, err := newSiaDirTestDir(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create New SiaDir that is two levels deep
	topDir := filepath.Join(testDir, "TestDir")
	subDir := "SubDir"
	path := filepath.Join(topDir, subDir)
	siaDir, err := New(path, testDir, persist.DefaultDiskPermissionsTest)
	if err != nil {
		t.Fatal(err)
	}

	// Check Sub Dir
	//
	// Check that the metadata was initialized properly
	md := siaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(path, modules.SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}

	// Check Top Directory
	//
	// Check that the directory and .siadir file were created on disk
	_, err = os.Stat(topDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(topDir, modules.SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}
	// Get SiaDir
	topSiaDir, err := LoadSiaDir(topDir, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the metadata was initialized properly
	md = topSiaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Check Root Directory
	//
	// Get SiaDir
	rootSiaDir, err := LoadSiaDir(testDir, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	// Check that the metadata was initialized properly
	md = rootSiaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Check that the directory and the .siadir file were created on disk
	_, err = os.Stat(testDir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(testDir, modules.SiaDirExtension))
	if err != nil {
		t.Fatal(err)
	}
}

// testSiaDirDelete verifies the SiaDir performs as expected after a delete
func testSiaDirDelete(t *testing.T) {
	// Create new siaDir
	rootDir, err := newRootDir(t)
	if err != nil {
		t.Fatal(err)
	}
	siaPath, err := modules.NewSiaPath("deleteddir")
	if err != nil {
		t.Fatal(err)
	}
	siaDirSysPath := siaPath.SiaDirSysPath(rootDir)
	siaDir, err := New(siaDirSysPath, rootDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the siadir and keep siadir in memory
	err = siaDir.Delete()
	if err != nil {
		t.Fatal(err)
	}

	// Path should be gone
	_, err = os.Stat(siaDir.path)
	if !os.IsNotExist(err) {
		t.Error("unexpected error", err)
	}

	// Verify functions either return or error accordingly
	//
	// First set should not error or panic
	if !siaDir.Deleted() {
		t.Error("SiaDir metadata should reflect the deletion")
	}
	_ = siaDir.MDPath()
	_ = siaDir.Metadata()
	_ = siaDir.Path()

	// Second Set should return an error
	err = siaDir.Rename("")
	if !errors.Contains(err, ErrDeleted) {
		t.Error("Rename should return with and error for SiaDir deleted")
	}
	err = siaDir.SetPath("")
	if !errors.Contains(err, ErrDeleted) {
		t.Error("SetPath should return with and error for SiaDir deleted")
	}
	_, err = siaDir.DirReader()
	if !errors.Contains(err, ErrDeleted) {
		t.Error("DirReader should return with and error for SiaDir deleted")
	}
	siaDir.mu.Lock()
	err = siaDir.updateMetadata(Metadata{})
	if !errors.Contains(err, ErrDeleted) {
		t.Error("updateMetadata should return with and error for SiaDir deleted")
	}
	siaDir.mu.Unlock()
}

// testUpdateMetadata probes the UpdateMetadata methods
func testUpdateMetadata(t *testing.T) {
	// Create new siaDir
	rootDir, err := newRootDir(t)
	if err != nil {
		t.Fatal(err)
	}
	siaPath, err := modules.NewSiaPath("TestDir")
	if err != nil {
		t.Fatal(err)
	}
	siaDirSysPath := siaPath.SiaDirSysPath(rootDir)
	siaDir, err := New(siaDirSysPath, rootDir, modules.DefaultDirPerm)
	if err != nil {
		t.Fatal(err)
	}

	// Check metadata was initialized properly in memory and on disk
	md := siaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}
	siaDir, err = LoadSiaDir(siaDirSysPath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	md = siaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Set the metadata
	metadataUpdate := randomMetadata()

	err = siaDir.UpdateMetadata(metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Check that the metadata was updated properly in memory and on disk
	md = siaDir.metadata
	err = equalMetadatas(md, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}
	siaDir, err = LoadSiaDir(siaDirSysPath, modules.ProdDependencies)
	if err != nil {
		t.Fatal(err)
	}
	md = siaDir.metadata
	// Check Time separately due to how the time is persisted
	if !md.AggregateLastHealthCheckTime.Equal(metadataUpdate.AggregateLastHealthCheckTime) {
		t.Fatalf("AggregateLastHealthCheckTimes not equal, got %v expected %v", md.AggregateLastHealthCheckTime, metadataUpdate.AggregateLastHealthCheckTime)
	}
	metadataUpdate.AggregateLastHealthCheckTime = md.AggregateLastHealthCheckTime
	if !md.LastHealthCheckTime.Equal(metadataUpdate.LastHealthCheckTime) {
		t.Fatalf("LastHealthCheckTimes not equal, got %v expected %v", md.LastHealthCheckTime, metadataUpdate.LastHealthCheckTime)
	}
	metadataUpdate.LastHealthCheckTime = md.LastHealthCheckTime
	if !md.AggregateModTime.Equal(metadataUpdate.AggregateModTime) {
		t.Fatalf("AggregateModTimes not equal, got %v expected %v", md.AggregateModTime, metadataUpdate.AggregateModTime)
	}
	metadataUpdate.AggregateModTime = md.AggregateModTime
	if !md.ModTime.Equal(metadataUpdate.ModTime) {
		t.Fatalf("ModTimes not equal, got %v expected %v", md.ModTime, metadataUpdate.ModTime)
	}
	metadataUpdate.ModTime = md.ModTime
	// Check the rest of the metadata
	err = equalMetadatas(md, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// TODO Add checks for other update metadata methods
}
