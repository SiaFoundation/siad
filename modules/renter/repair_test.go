package renter

import (
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// equalBubbledMetadata is a helper that checks for equality in the siadir
// metadata that gets bubbled
func equalBubbledMetadata(md1, md2 siadir.Metadata) error {
	// Check AggregateHealth
	if md1.AggregateHealth != md2.AggregateHealth {
		return fmt.Errorf("AggregateHealth not equal, %v and %v", md1.AggregateHealth, md2.AggregateHealth)
	}
	// Check AggregateNumFiles
	if md1.AggregateNumFiles != md2.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md1.AggregateNumFiles, md2.AggregateNumFiles)
	}
	// Check Size
	if md1.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("aggregate sizes not equal, %v and %v", md1.AggregateSize, md2.AggregateSize)
	}
	// Check Health
	if md1.Health != md2.Health {
		return fmt.Errorf("healths not equal, %v and %v", md1.Health, md2.Health)
	}
	// Check LastHealthCheckTimes
	if md2.LastHealthCheckTime != md1.LastHealthCheckTime {
		return fmt.Errorf("LastHealthCheckTimes not equal %v and %v", md2.LastHealthCheckTime, md1.LastHealthCheckTime)
	}
	// Check MinRedundancy
	if md1.MinRedundancy != md2.MinRedundancy {
		return fmt.Errorf("MinRedundancy not equal, %v and %v", md1.MinRedundancy, md2.MinRedundancy)
	}
	// Check Mod Times
	if md2.ModTime != md1.ModTime {
		return fmt.Errorf("ModTimes not equal %v and %v", md2.ModTime, md1.ModTime)
	}
	// Check NumFiles
	if md1.NumFiles != md2.NumFiles {
		return fmt.Errorf("NumFiles not equal, %v and %v", md1.NumFiles, md2.NumFiles)
	}
	// Check NumStuckChunks
	if md1.NumStuckChunks != md2.NumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", md1.NumStuckChunks, md2.NumStuckChunks)
	}
	// Check NumSubDirs
	if md1.NumSubDirs != md2.NumSubDirs {
		return fmt.Errorf("NumSubDirs not equal, %v and %v", md1.NumSubDirs, md2.NumSubDirs)
	}
	// Check StuckHealth
	if md1.StuckHealth != md2.StuckHealth {
		return fmt.Errorf("stuck healths not equal, %v and %v", md1.StuckHealth, md2.StuckHealth)
	}
	return nil
}

// TestBubbleHealth tests to make sure that the health of the most in need file
// in a directory is bubbled up to the right levels and probes the supporting
// functions as well
func TestBubbleHealth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Check to make sure bubble doesn't error on an empty directory
	err = rt.renter.managedBubbleMetadata(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	defaultMetadata := siadir.Metadata{
		AggregateHealth:     siadir.DefaultDirHealth,
		Health:              siadir.DefaultDirHealth,
		StuckHealth:         siadir.DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		NumStuckChunks:      0,
	}
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Health
		if err = equalBubbledMetadata(metadata, defaultMetadata); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create a test directory with the following healths
	//
	// root/ 1
	// root/SubDir1/ 1
	// root/SubDir1/SubDir1/ 1
	// root/SubDir1/SubDir2/ 4

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	subDir1_1, err := subDir1.Join(subDir1.String())
	if err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}

	// Set Healths of all the directories so they are not the defaults
	//
	// NOTE: You cannot set the NumStuckChunks to a non zero number without a
	// file in the directory as this will create a developer error
	var siaPath modules.SiaPath
	checkTime := time.Now()
	metadataUpdate := siadir.Metadata{
		AggregateHealth:     1,
		Health:              1,
		StuckHealth:         0,
		LastHealthCheckTime: checkTime,
	}
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(modules.RootSiaPath(), metadataUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath, _ = subDir1.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath, _ = subDir1_1.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}
	// Set health of subDir1/subDir2 to be the worst and set the
	siaPath, _ = subDir1_2.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	metadataUpdate.Health = 4
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of the directory that has the worst pre set health
	// subDir1/subDir2, the health that gets bubbled should be the health of
	// subDir1/subDir1 since subDir1/subDir2 is empty meaning it's calculated
	// health will return to the default health, even through we set the health
	// to be the worst health
	//
	// Note: this tests the edge case of bubbling an empty directory and
	// directories with no files but do have sub directories since bubble will
	// execute on all the parent directories
	rt.renter.managedBubbleMetadata(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Compare to metadata of subDir1/subDir1
		expectedHealth, err := rt.renter.managedDirectoryMetadata(subDir1_1)
		if err != nil {
			return err
		}
		if err = equalBubbledMetadata(metadata, expectedHealth); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a file to the lowest level
	//
	// Worst health with current erasure coding is 2 = (1 - (0-1)/1)
	rsc, _ := siafile.NewRSCode(1, 1)
	siaPath, err = subDir1_2.Join("test")
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     siaPath,
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Since we are just adding the file, no chunks will have been uploaded
	// meaning the health of the file should be the worst case health. Now the
	// health that is bubbled up should be the health of the file added to
	// subDir1/subDir2
	//
	// Note: this tests the edge case of bubbling a directory with a file
	// but no sub directories
	offline, goodForRenew, _ := rt.renter.managedRenterContractsAndUtilities([]*filesystem.FileNode{f})
	fileHealth, _, _, _, _ := f.Health(offline, goodForRenew)
	if fileHealth != 2 {
		t.Fatalf("Expected heath to be 2, got %v", fileHealth)
	}

	// Mark the file as stuck by marking one of its chunks as stuck
	f.SetStuck(0, true)

	// Now when we bubble the health and check for the worst health we should still see
	// that the health is the health of subDir1/subDir1 which was set to 1 again
	// and the stuck health will be the health of the stuck file
	rt.renter.managedBubbleMetadata(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Compare to metadata of subDir1/subDir1
		expectedHealth, err := rt.renter.managedDirectoryMetadata(subDir1_1)
		if err != nil {
			return err
		}
		expectedHealth.StuckHealth = 2
		expectedHealth.NumStuckChunks++
		if err = equalBubbledMetadata(metadata, expectedHealth); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mark the file as un-stuck
	f.SetStuck(0, false)

	// Now if we bubble the health and check for the worst health we should see
	// that the health is the health of the file
	rt.renter.managedBubbleMetadata(siaPath)
	expectedHealth := siadir.Metadata{
		Health:      2,
		StuckHealth: 0,
	}
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Health
		if err = equalBubbledMetadata(health, expectedHealth); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a sub directory to the directory that contains the file that has a
	// worst health than the file and confirm that health gets bubbled up.
	//
	// Note: this tests the edge case of bubbling a directory that has both a
	// file and a sub directory
	subDir1_2_1, err := subDir1_2.Join(subDir1.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2_1); err != nil {
		t.Fatal(err)
	}
	// Reset metadataUpdate with expected values
	expectedHealth = siadir.Metadata{
		AggregateHealth:     4,
		Health:              4,
		StuckHealth:         0,
		LastHealthCheckTime: time.Now(),
	}
	siaPath, _ = subDir1_2_1.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(siaPath, expectedHealth); err != nil {
		t.Fatal(err)
	}
	rt.renter.managedBubbleMetadata(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Health
		if err = equalBubbledMetadata(health, expectedHealth); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestOldestHealthCheckTime probes managedOldestHealthCheckTime to verify that
// the directory with the oldest LastHealthCheckTime is returned
func TestOldestHealthCheckTime(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test directory with sub folders
	//
	// root/ 1
	// root/SubDir1/
	// root/SubDir1/SubDir2/
	// root/SubDir2/

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}

	// Set the LastHealthCheckTime of SubDir1/SubDir2 to be the oldest
	oldestCheckTime := time.Now().AddDate(0, 0, -1)
	oldestHealthCheckUpdate := siadir.Metadata{
		Health:              1,
		StuckHealth:         0,
		LastHealthCheckTime: oldestCheckTime,
	}
	subDir1_2, _ = subDir1_2.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err := rt.renter.staticFileSystem.UpdateDirMetadata(subDir1_2, oldestHealthCheckUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of SubDir1 so that the oldest LastHealthCheckTime of
	// SubDir1/SubDir2 gets bubbled up
	rt.renter.managedBubbleMetadata(subDir1)

	// Find the oldest directory, should be SubDir1/SubDir2
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, lastCheck, err := rt.renter.managedOldestHealthCheckTime()
		if err != nil {
			return err
		}
		if dir.Equals(subDir1_2) {
			return fmt.Errorf("Expected to find %v but found %v", subDir1_2.String(), dir.String())
		}
		if !lastCheck.Equal(oldestCheckTime) {
			return fmt.Errorf("Expected to find time of %v but found %v", oldestCheckTime, lastCheck)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestNumFiles verifies that the number of files and aggregate number of files
// is accurately reported
func TestNumFiles(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test directory with sub folders
	//
	// root/ file
	// root/SubDir1/
	// root/SubDir1/SubDir2/ file

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}
	// Add files
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}

	// Call bubble on lowest lever and confirm top level reports accurate number
	// of files and aggregate number of files
	rt.renter.managedBubbleMetadata(subDir1_2)
	build.Retry(100, 100*time.Millisecond, func() error {
		dirInfo, err := rt.renter.staticFileSystem.DirInfo(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if dirInfo.NumFiles != 1 {
			return fmt.Errorf("NumFiles incorrect, got %v expected %v", dirInfo.NumFiles, 1)
		}
		if dirInfo.AggregateNumFiles != 2 {
			return fmt.Errorf("AggregateNumFiles incorrect, got %v expected %v", dirInfo.AggregateNumFiles, 2)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDirectorySize verifies that the Size of a directory is accurately
// reported
func TestDirectorySize(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test directory with sub folders
	//
	// root/ file
	// root/SubDir1/
	// root/SubDir1/SubDir2/ file

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}
	// Add files
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	fileSize := uint64(100)
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}

	// Call bubble on lowest lever and confirm top level reports accurate size
	rt.renter.managedBubbleMetadata(subDir1_2)
	build.Retry(100, 100*time.Millisecond, func() error {
		dirInfo, err := rt.renter.staticFileSystem.DirInfo(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if dirInfo.AggregateSize != 3*fileSize {
			return fmt.Errorf("AggregateSize incorrect, got %v expected %v", dirInfo.AggregateSize, 3*fileSize)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestDirectoryModTime verifies that the last update time of a directory is
// accurately reported
func TestDirectoryModTime(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create a test directory with sub folders
	//
	// root/ file
	// root/SubDir1/
	// root/SubDir1/SubDir2/ file

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}
	// Add files
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	fileSize := uint64(100)
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Call bubble on lowest lever and confirm top level reports accurate last
	// update time
	rt.renter.managedBubbleMetadata(subDir1_2)
	build.Retry(100, 100*time.Millisecond, func() error {
		dirInfo, err := rt.renter.staticFileSystem.DirInfo(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if dirInfo.MostRecentModTime != f.ModTime() {
			return fmt.Errorf("ModTime is incorrect, got %v expected %v", dirInfo.MostRecentModTime, f.ModTime())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRandomStuckDirectory probes managedStuckDirectory to make sure it
// randomly picks a correct directory
func TestRandomStuckDirectory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create a test directory with sub folders
	//
	// root/home/siafiles/
	// root/home/siafiles/SubDir1/
	// root/home/siafiles/SubDir1/SubDir2/
	// root/home/siafiles/SubDir2/
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2); err != nil {
		t.Fatal(err)
	}

	// Add a file to siafiles and SubDir1/SubDir2 and mark the first chunk as
	// stuck in each file
	//
	// This will test the edge case of continuing to find stuck files when a
	// directory has no files only directories
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	sp, _ := up.SiaPath.Rebase(modules.RootSiaPath(), modules.UserSiaPath())

	err = rt.renter.staticFileSystem.NewSiaFile(sp, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rt.renter.staticFileSystem.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.SetFileStuck(up.SiaPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}
	f.Close()
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	sp, _ = up.SiaPath.Rebase(modules.RootSiaPath(), modules.UserSiaPath())
	err = rt.renter.staticFileSystem.NewSiaFile(sp, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err = rt.renter.staticFileSystem.OpenSiaFile(sp)
	if err != nil {
		t.Fatal(err)
	}
	err = f.GrowNumChunks(2)
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.SetFileStuck(up.SiaPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}
	f.Close()

	// Bubble directory information so NumStuckChunks is updated, there should
	// be at least 3 stuck chunks because of the 3 we manually marked as stuck,
	// but the repair loop could have marked the rest as stuck so we just want
	// to ensure that the root directory reflects at least the 3 we marked as
	// stuck
	subDir1_2, _ = subDir1_2.Rebase(modules.RootSiaPath(), modules.UserSiaPath())
	rt.renter.managedBubbleMetadata(subDir1_2)
	err = build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Metadata
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Aggregate number of stuck chunks
		if metadata.AggregateNumStuckChunks != uint64(3) {
			return fmt.Errorf("Incorrect number of stuck chunks, should be 3")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Find a stuck directory randomly, it should never find root/SubDir1 or
	// root/SubDir2 and should find root/SubDir1/SubDir2 more than root
	var count1_2, countRoot, countSiaFiles int
	for i := 0; i < 100; i++ {
		dir, err := rt.renter.managedStuckDirectory()
		if err != nil {
			t.Fatal(err)
		}
		if dir.Equals(subDir1_2) {
			count1_2++
			continue
		}
		if dir.Equals(modules.RootSiaPath()) {
			countRoot++
			continue
		}
		if dir.Equals(modules.UserSiaPath()) {
			countSiaFiles++
			continue
		}
		t.Fatal("Unstuck dir found", dir.String())
	}

	// Randomness is weighted so we should always find file 1 more often
	if countRoot > count1_2 {
		t.Log("Should have found root/SubDir1/SubDir2 more than root")
		t.Fatalf("Found root/SubDir1/SubDir2 %v times and root %v times", count1_2, countRoot)
	}
	// If we never find root/SubDir1/SubDir2 then that is a failure
	if count1_2 == 0 {
		t.Fatal("Found root/SubDir1/SubDir2 0 times")
	}
	// If we never find root that is not ideal, Log this error. If it happens
	// a lot then the weighted randomness should be improved
	if countRoot == 0 {
		t.Logf("Found root 0 times. Consider improving the weighted randomness")
	}
	t.Log("Found root/SubDir1/SubDir2", count1_2, "times and root", countRoot, "times")
}

// TestRandomStuckFile tests that the renter can randomly find stuck files
// weighted by the number of stuck chunks
func TestRandomStuckFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create 3 files at root
	//
	// File 1 will have all chunks stuck
	file1, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	err = file1.GrowNumChunks(3)
	if err != nil {
		t.Fatal(err)
	}
	siaPath1 := rt.renter.staticFileSystem.FileSiaPath(file1)
	sp1, _ := siaPath1.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	err = rt.renter.SetFileStuck(sp1, true)
	if err != nil {
		t.Fatal(err)
	}

	// File 2 will have only 1 chunk stuck
	file2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath2 := rt.renter.staticFileSystem.FileSiaPath(file2)
	sp2, _ := siaPath2.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	err = file2.SetStuck(0, true)
	if err != nil {
		t.Fatal(err)
	}

	// File 3 will be unstuck
	file3, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath3 := rt.renter.staticFileSystem.FileSiaPath(file3)
	sp3, _ := siaPath3.Rebase(modules.UserSiaPath(), modules.RootSiaPath())

	// Since we disabled the health loop for this test, call it manually to
	// update the directory metadata
	err = rt.renter.managedBubbleMetadata(modules.UserSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	build.Retry(100, 100*time.Millisecond, func() error {
		i++
		if i%10 == 0 {
			err = rt.renter.managedBubbleMetadata(modules.RootSiaPath())
			if err != nil {
				return err
			}
		}
		// Get Root Directory Metadata
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Aggregate number of stuck chunks
		if metadata.AggregateNumStuckChunks == 0 {
			return errors.New("no stuck chunks found")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFindRandomFile(t, rt.renter, modules.UserSiaPath(), siaPath1, siaPath2, siaPath3)

	// Create a directory
	dir, err := modules.NewSiaPath("Dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(dir); err != nil {
		t.Fatal(err)
	}

	// Move siafiles to dir
	newSiaPath1, err := dir.Join(sp1.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(sp1, newSiaPath1)
	if err != nil {
		t.Fatal(err)
	}
	newSiaPath2, err := dir.Join(sp2.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(sp2, newSiaPath2)
	if err != nil {
		t.Fatal(err)
	}
	newSiaPath3, err := dir.Join(sp3.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(sp3, newSiaPath3)
	if err != nil {
		t.Fatal(err)
	}

	dir, _ = dir.Rebase(modules.RootSiaPath(), modules.UserSiaPath())
	newSiaPath1, _ = newSiaPath1.Rebase(modules.RootSiaPath(), modules.UserSiaPath())
	newSiaPath2, _ = newSiaPath2.Rebase(modules.RootSiaPath(), modules.UserSiaPath())
	newSiaPath3, _ = newSiaPath3.Rebase(modules.RootSiaPath(), modules.UserSiaPath())

	// Since we disabled the health loop for this test, call it manually to
	// update the directory metadata
	err = rt.renter.managedBubbleMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	i = 0
	build.Retry(100, 100*time.Millisecond, func() error {
		i++
		if i%10 == 0 {
			err = rt.renter.managedBubbleMetadata(dir)
			if err != nil {
				return err
			}
		}
		// Get Directory Metadata
		metadata, err := rt.renter.managedDirectoryMetadata(dir)
		if err != nil {
			return err
		}
		// Check Aggregate number of stuck chunks
		if metadata.AggregateNumStuckChunks == 0 {
			return errors.New("no stuck chunks found")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	checkFindRandomFile(t, rt.renter, dir, newSiaPath1, newSiaPath2, newSiaPath3)
}

// checkFindRandomFile is a helper function that checks the output from
// managedStuckFile in a loop
func checkFindRandomFile(t *testing.T, r *Renter, dir, siaPath1, siaPath2, siaPath3 modules.SiaPath) {
	// Find a stuck file randomly, it should never find file 3 and should find
	// file 1 more than file 2.
	var count1, count2 int
	for i := 0; i < 100; i++ {
		siaPath, err := r.managedStuckFile(dir)
		if err != nil {
			t.Fatal(err)
		}
		if siaPath.Equals(siaPath1) {
			count1++
		}
		if siaPath.Equals(siaPath2) {
			count2++
		}
		if siaPath.Equals(siaPath3) {
			t.Fatal("Unstuck file 3 found")
		}
	}

	// Randomness is weighted so we should always find file 1 more often
	if count2 > count1 {
		t.Log("Should have found file 1 more than file 2")
		t.Fatalf("Found file 1 %v times and file 2 %v times", count1, count2)
	}
	// If we never find file 1 then that is a failure
	if count1 == 0 {
		t.Fatal("Found file 1 0 times")
	}
	// If we never find file 2 that is not ideal, Log this error. If it happens
	// a lot then the weighted randomness should be improved
	if count2 == 0 {
		t.Logf("Found file 2 0 times. Consider improving the weighted randomness")
	}
	t.Log("Found file1", count1, "times and file2", count2, "times")
}

// TestCalculateFileMetadata checks that the values returned from
// managedCalculateFileMetadata make sense
func TestCalculateFileMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create a file
	rsc, _ := siafile.NewRSCode(1, 1)
	siaPath, err := modules.NewSiaPath("rootFile")
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     siaPath,
		ErasureCode: rsc,
	}
	fileSize := uint64(100)
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, 0777, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Grab initial metadata values
	offline, goodForRenew, _ := rt.renter.managedRenterContractsAndUtilities([]*filesystem.FileNode{sf})
	health, stuckHealth, _, _, numStuckChunks := sf.Health(offline, goodForRenew)
	redundancy, _, err := sf.Redundancy(offline, goodForRenew)
	if err != nil {
		t.Fatal(err)
	}
	lastHealthCheckTime := sf.LastHealthCheckTime()
	modTime := sf.ModTime()

	// Check calculated metadata
	fileMetadata, err := rt.renter.managedCalculateAndUpdateFileMetadata(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Check siafile calculated metadata
	if fileMetadata.Health != health {
		t.Fatalf("health incorrect, expected %v got %v", health, fileMetadata.Health)
	}
	if fileMetadata.StuckHealth != stuckHealth {
		t.Fatalf("stuckHealth incorrect, expected %v got %v", stuckHealth, fileMetadata.StuckHealth)
	}
	if fileMetadata.Redundancy != redundancy {
		t.Fatalf("redundancy incorrect, expected %v got %v", redundancy, fileMetadata.Redundancy)
	}
	if fileMetadata.Size != fileSize {
		t.Fatalf("size incorrect, expected %v got %v", fileSize, fileMetadata.Size)
	}
	if fileMetadata.NumStuckChunks != numStuckChunks {
		t.Fatalf("numstuckchunks incorrect, expected %v got %v", numStuckChunks, fileMetadata.NumStuckChunks)
	}
	if fileMetadata.LastHealthCheckTime.Equal(lastHealthCheckTime) || fileMetadata.LastHealthCheckTime.IsZero() {
		t.Log("Initial lasthealthchecktime", lastHealthCheckTime)
		t.Log("Calculated lasthealthchecktime", fileMetadata.LastHealthCheckTime)
		t.Fatal("Expected lasthealthchecktime to have updated and be non zero")
	}
	if !fileMetadata.ModTime.Equal(modTime) {
		t.Fatalf("Unexpected modtime, expected %v got %v", modTime, fileMetadata.ModTime)
	}
}

// TestCreateMissingSiaDir confirms that the repair code creates a siadir file
// if one is not found
func TestCreateMissingSiaDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Confirm the siadir file is on disk
	siaDirPath := modules.RootSiaPath().SiaDirMetadataSysPath(rt.renter.staticFileSystem.Root())
	_, err = os.Stat(siaDirPath)
	if err != nil {
		t.Fatal(err)
	}

	// Remove .siadir file on disk
	err = os.Remove(siaDirPath)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm siadir is gone
	_, err = os.Stat(siaDirPath)
	if !os.IsNotExist(err) {
		t.Fatal("Err should have been IsNotExist", err)
	}

	// Create siadir file with managedDirectoryMetadata
	_, err = rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Confirm it is on disk
	_, err = os.Stat(siaDirPath)
	if err != nil {
		t.Fatal(err)
	}
}

// TestAddStuckChunksToHeap probes the managedAddStuckChunksToHeap method
func TestAddStuckChunksToHeap(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// create renter with dependencies, first to disable the background health,
	// repair, and stuck loops from running, then update it to bypass the worker
	// pool length check in managedBuildUnfinishedChunks
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}

	// create file with no stuck chunks
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777, false)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Create maps for method inputs
	hosts := make(map[string]struct{})
	offline := make(map[string]bool)
	goodForRenew := make(map[string]bool)

	// Manually add workers to worker pool
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.staticWorkerPool.mu.Lock()
		rt.renter.staticWorkerPool.workers[string(i)] = &worker{
			killChan: make(chan struct{}),
			wakeChan: make(chan struct{}, 1),
		}
		rt.renter.staticWorkerPool.mu.Unlock()
	}

	// call managedAddStuckChunksToHeap, no chunks should be added
	err = rt.renter.managedAddStuckChunksToHeap(up.SiaPath, hosts, offline, goodForRenew)
	if err != errNoStuckChunks {
		t.Fatal(err)
	}
	if rt.renter.uploadHeap.managedLen() != 0 {
		t.Fatal("Expected uploadHeap to be of length 0 got", rt.renter.uploadHeap.managedLen())
	}

	// make chunk stuck
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}

	// call managedAddStuckChunksToHeap, chunk should be added to heap
	err = rt.renter.managedAddStuckChunksToHeap(up.SiaPath, hosts, offline, goodForRenew)
	if err != nil {
		t.Fatal(err)
	}
	if rt.renter.uploadHeap.managedLen() != 1 {
		t.Fatal("Expected uploadHeap to be of length 1 got", rt.renter.uploadHeap.managedLen())
	}

	// Pop chunk, chunk should be marked as fileRecentlySuccessful true
	chunk := rt.renter.uploadHeap.managedPop()
	if !chunk.fileRecentlySuccessful {
		t.Fatal("chunk not marked as fileRecentlySuccessful true")
	}
}
