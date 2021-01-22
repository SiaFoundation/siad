package siadir

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// checkMetadataInit is a helper that verifies that the metadata was initialized
// properly
func checkMetadataInit(md Metadata) error {
	// Check that the modTimes are not Zero
	if md.AggregateModTime.IsZero() {
		return errors.New("AggregateModTime not initialized")
	}
	if md.ModTime.IsZero() {
		return errors.New("ModTime not initialized")
	}

	// All the rest of the metadata should be default values
	initMetadata := Metadata{
		AggregateHealth:        DefaultDirHealth,
		AggregateMinRedundancy: DefaultDirRedundancy,
		AggregateModTime:       md.AggregateModTime,
		AggregateRemoteHealth:  DefaultDirHealth,
		AggregateStuckHealth:   DefaultDirHealth,

		Health:        DefaultDirHealth,
		MinRedundancy: DefaultDirRedundancy,
		ModTime:       md.ModTime,
		RemoteHealth:  DefaultDirHealth,
		StuckHealth:   DefaultDirHealth,
	}

	return equalMetadatas(md, initMetadata)
}

// newRootDir creates a root directory for the test and removes old test files
func newRootDir(t *testing.T) (string, error) {
	dir := filepath.Join(os.TempDir(), "siadirs", t.Name())
	err := os.RemoveAll(dir)
	if err != nil {
		return "", err
	}
	return dir, nil
}

// randomMetadata returns a siadir Metadata struct with random values set
func randomMetadata() Metadata {
	md := Metadata{
		AggregateHealth:              float64(fastrand.Intn(100)),
		AggregateLastHealthCheckTime: time.Now(),
		AggregateMinRedundancy:       float64(fastrand.Intn(100)),
		AggregateModTime:             time.Now(),
		AggregateNumFiles:            fastrand.Uint64n(100),
		AggregateNumStuckChunks:      fastrand.Uint64n(100),
		AggregateNumSubDirs:          fastrand.Uint64n(100),
		AggregateRemoteHealth:        float64(fastrand.Intn(100)),
		AggregateRepairSize:          fastrand.Uint64n(100),
		AggregateSize:                fastrand.Uint64n(100),
		AggregateStuckHealth:         float64(fastrand.Intn(100)),

		AggregateSkynetFiles: fastrand.Uint64n(100),
		AggregateSkynetSize:  fastrand.Uint64n(100),

		Health:              float64(fastrand.Intn(100)),
		LastHealthCheckTime: time.Now(),
		MinRedundancy:       float64(fastrand.Intn(100)),
		ModTime:             time.Now(),
		NumFiles:            fastrand.Uint64n(100),
		NumStuckChunks:      fastrand.Uint64n(100),
		NumSubDirs:          fastrand.Uint64n(100),
		RemoteHealth:        float64(fastrand.Intn(100)),
		RepairSize:          fastrand.Uint64n(100),
		Size:                fastrand.Uint64n(100),
		StuckHealth:         float64(fastrand.Intn(100)),

		SkynetFiles: fastrand.Uint64n(100),
		SkynetSize:  fastrand.Uint64n(100),
	}
	return md
}

// TestNewSiaDir tests that siadirs are created on disk properly. It uses
// LoadSiaDir to read the metadata from disk
//func TestNewSiaDir(t *testing.T) {
//	if testing.Short() {
//		t.SkipNow()
//	}
//	t.Parallel()
//
//	// Create New SiaDir that is two levels deep
//	rootDir, err := newRootDir(t)
//	if err != nil {
//		t.Fatal(err)
//	}
//	siaPathDir, err := modules.NewSiaPath("TestDir")
//	if err != nil {
//		t.Fatal(err)
//	}
//	siaPathSubDir, err := modules.NewSiaPath("SubDir")
//	if err != nil {
//		t.Fatal(err)
//	}
//	siaPath, err := siaPathDir.Join(siaPathSubDir.String())
//	if err != nil {
//		t.Fatal(err)
//	}
//	wal, _ := newTestWAL()
//	siaDir, err := New(siaPath, rootDir, wal)
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	// Check Sub Dir
//	//
//	// Check that the metadta was initialized properly
//	md := siaDir.metadata
//	if err = checkMetadataInit(md); err != nil {
//		t.Fatal(err)
//	}
//	// Check that the SiaPath was initialized properly
//	if siaDir.SiaPath() != siaPath {
//		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", siaDir.SiaPath(), siaPath)
//	}
//	// Check that the directory and .siadir file were created on disk
//	_, err = os.Stat(siaPath.SiaDirSysPath(rootDir))
//	if err != nil {
//		t.Fatal(err)
//	}
//	_, err = os.Stat(siaPath.SiaDirMetadataSysPath(rootDir))
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	// Check Top Directory
//	//
//	// Check that the directory and .siadir file were created on disk
//	_, err = os.Stat(siaPath.SiaDirSysPath(rootDir))
//	if err != nil {
//		t.Fatal(err)
//	}
//	_, err = os.Stat(siaPath.SiaDirMetadataSysPath(rootDir))
//	if err != nil {
//		t.Fatal(err)
//	}
//	// Get SiaDir
//	subDir, err := LoadSiaDir(rootDir, siaPathDir, modules.ProdDependencies, wal)
//	// Check that the metadata was initialized properly
//	md = subDir.metadata
//	if err = checkMetadataInit(md); err != nil {
//		t.Fatal(err)
//	}
//	// Check that the SiaPath was initialized properly
//	if subDir.SiaPath() != siaPathDir {
//		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", subDir.SiaPath(), siaPathDir)
//	}
//
//	// Check Root Directory
//	//
//	// Get SiaDir
//	rootSiaDir, err := LoadSiaDir(rootDir, modules.RootSiaPath(), modules.ProdDependencies, wal)
//	// Check that the metadata was initialized properly
//	md = rootSiaDir.metadata
//	if err = checkMetadataInit(md); err != nil {
//		t.Fatal(err)
//	}
//	// Check that the SiaPath was initialized properly
//	if !rootSiaDir.SiaPath().IsRoot() {
//		t.Fatalf("SiaDir SiaPath not set properly: got %v expected %v", rootSiaDir.SiaPath().String(), "")
//	}
//	// Check that the directory and .siadir file were created on disk
//	_, err = os.Stat(rootDir)
//	if err != nil {
//		t.Fatal(err)
//	}
//	_, err = os.Stat(modules.RootSiaPath().SiaDirMetadataSysPath(rootDir))
//	if err != nil {
//		t.Fatal(err)
//	}
//}

// Test UpdatedMetadata probes the UpdateMetadata method
func TestUpdateMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

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
	wal, _ := newTestWAL()
	siaDir, err := New(siaDirSysPath, rootDir, modules.DefaultDirPerm, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Check metadata was initialized properly in memory and on disk
	md := siaDir.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}
	siaDir, err = LoadSiaDir(siaDirSysPath, modules.ProdDependencies, wal)
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
	siaDir, err = LoadSiaDir(siaDirSysPath, modules.ProdDependencies, wal)
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
}

// TestSiaDirDelete verifies the SiaDir performs as expected after a delete
func TestSiaDirDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

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
	wal, _ := newTestWAL()
	siaDir, err := New(siaDirSysPath, rootDir, modules.DefaultDirPerm, wal)
	if err != nil {
		t.Fatal(err)
	}

	// Delete the siadir and keep siadir in memory
	err = siaDir.Delete()
	if err != nil {
		t.Fatal(err)
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
