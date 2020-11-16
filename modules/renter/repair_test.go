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
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem/siadir"
	"gitlab.com/NebulousLabs/Sia/persist"
	"gitlab.com/NebulousLabs/Sia/siatest/dependencies"
)

// equalBubbledMetadata is a helper that checks for equality in the siadir
// metadata that gets bubbled
// Since we can't check timestamps for equality cause they are often set to
// `time.Now()` by methods, we allow a timestamp to be off by a certain delta.
func equalBubbledMetadata(md1, md2 siadir.Metadata, delta time.Duration) error {
	timeEquals := func(t1, t2 time.Time) bool {
		if t1.After(t2) && t1.After(t2.Add(delta)) {
			return false
		}
		if t2.After(t1) && t2.After(t1.Add(delta)) {
			return false
		}
		return true
	}
	// Check AggregateHealth
	if md1.AggregateHealth != md2.AggregateHealth {
		return fmt.Errorf("AggregateHealth not equal, %v and %v", md1.AggregateHealth, md2.AggregateHealth)
	}
	// Check AggregateNumFiles
	if md1.AggregateNumFiles != md2.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md1.AggregateNumFiles, md2.AggregateNumFiles)
	}
	// Check AggregateSize
	if md1.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("aggregate sizes not equal, %v and %v", md1.AggregateSize, md2.AggregateSize)
	}
	// Check AggregateHealth
	if md1.AggregateHealth != md2.AggregateHealth {
		return fmt.Errorf("AggregateHealths not equal, %v and %v", md1.AggregateHealth, md2.AggregateHealth)
	}
	// Check AggregateLastHealthCheckTimes
	if !timeEquals(md2.AggregateLastHealthCheckTime, md1.AggregateLastHealthCheckTime) {
		return fmt.Errorf("AggregateLastHealthCheckTimes not equal %v and %v (%v)", md1.AggregateLastHealthCheckTime, md2.AggregateLastHealthCheckTime, delta)
	}
	// Check MinRedundancy
	if md1.AggregateMinRedundancy != md2.AggregateMinRedundancy {
		return fmt.Errorf("AggregateMinRedundancy not equal, %v and %v", md1.AggregateMinRedundancy, md2.AggregateMinRedundancy)
	}
	// Check Mod Times
	if !timeEquals(md2.AggregateModTime, md1.AggregateModTime) {
		return fmt.Errorf("AggregateModTimes not equal %v and %v (%v)", md1.AggregateModTime, md2.AggregateModTime, delta)
	}
	// Check AggregateNumFiles
	if md1.AggregateNumFiles != md2.AggregateNumFiles {
		return fmt.Errorf("AggregateNumFiles not equal, %v and %v", md1.AggregateNumFiles, md2.AggregateNumFiles)
	}
	// Check AggregateNumStuckChunks
	if md1.AggregateNumStuckChunks != md2.AggregateNumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", md1.AggregateNumStuckChunks, md2.AggregateNumStuckChunks)
	}
	// Check AggregateNumSubDirs
	if md1.AggregateNumSubDirs != md2.AggregateNumSubDirs {
		return fmt.Errorf("AggregateNumSubDirs not equal, %v and %v", md1.AggregateNumSubDirs, md2.AggregateNumSubDirs)
	}
	// Check AggregateSize
	if md1.AggregateSize != md2.AggregateSize {
		return fmt.Errorf("sizes not equal, %v and %v", md1.AggregateSize, md2.AggregateSize)
	}
	// Check AggregateStuckHealth
	if md1.AggregateStuckHealth != md2.AggregateStuckHealth {
		return fmt.Errorf("stuck healths not equal, %v and %v", md1.AggregateStuckHealth, md2.AggregateStuckHealth)
	}
	return nil
}

// openAndUpdateDir is a helper method for updating a siadir metadata
func (rt *renterTester) openAndUpdateDir(siapath modules.SiaPath, metadata siadir.Metadata) error {
	siadir, err := rt.renter.staticFileSystem.OpenSiaDir(siapath)
	if err != nil {
		return err
	}
	err = siadir.UpdateMetadata(metadata)
	return errors.Compose(err, siadir.Close())
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Bubble all the system dirs.
	beforeBubble := time.Now()
	err1 := rt.renter.managedBubbleMetadata(modules.BackupFolder)
	err2 := rt.renter.managedBubbleMetadata(modules.SkynetFolder)
	err3 := rt.renter.managedBubbleMetadata(modules.UserFolder)
	err = errors.Compose(err1, err2, err3)
	if err != nil {
		t.Fatal(err)
	}
	defaultMetadata := siadir.Metadata{
		AggregateHealth:              siadir.DefaultDirHealth,
		Health:                       siadir.DefaultDirHealth,
		StuckHealth:                  siadir.DefaultDirHealth,
		AggregateLastHealthCheckTime: beforeBubble,
		AggregateMinRedundancy:       -1,
		AggregateNumStuckChunks:      0,
		AggregateNumSubDirs:          5,
	}
	err = build.Retry(20, time.Second, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Set the ModTimes since those are not initialized until Bubble is Called
		defaultMetadata.AggregateModTime = metadata.AggregateModTime
		defaultMetadata.ModTime = metadata.ModTime
		// Check metadata
		if err = equalBubbledMetadata(metadata, defaultMetadata, time.Since(beforeBubble)); err != nil {
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
	if err := rt.renter.CreateDir(subDir1_1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}

	// Set Healths of all the directories so they are not the defaults
	//
	// NOTE: You cannot set the NumStuckChunks to a non zero number without a
	// file in the directory as this will create a developer error
	var siaPath modules.SiaPath
	checkTime := time.Now()
	metadataUpdate := siadir.Metadata{
		AggregateHealth:              1,
		Health:                       1,
		StuckHealth:                  0,
		AggregateLastHealthCheckTime: checkTime,
		LastHealthCheckTime:          checkTime,
	}
	// Create OpenAndUpdateDir helper method
	if err := rt.openAndUpdateDir(modules.RootSiaPath(), metadataUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath = subDir1
	if err := rt.openAndUpdateDir(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath = subDir1_1
	if err := rt.openAndUpdateDir(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}
	// Set health of subDir1/subDir2 to be the worst and set the
	siaPath = subDir1_2
	metadataUpdate.Health = 4
	if err := rt.openAndUpdateDir(siaPath, metadataUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of the directory that has the worst pre set health
	// subDir1/subDir2, the health that gets bubbled should be the health of
	// subDir1/subDir1 since subDir1/subDir2 is empty meaning it's calculated
	// health will return to the default health, even though we set the health
	// to be the worst health
	//
	// Note: this tests the edge case of bubbling an empty directory and
	// directories with no files but do have sub directories since bubble will
	// execute on all the parent directories
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	expectedMetadata, err := rt.renter.managedDirectoryMetadata(subDir1_1)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(20, time.Second, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Compare the specific metadata fields we expected to be bubbled from
		// subDir1/subDir1 to root
		if metadata.AggregateHealth != expectedMetadata.AggregateHealth {
			return fmt.Errorf("AggregateHealth not equal; %v and %v", metadata.AggregateHealth, expectedMetadata.AggregateHealth)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add a file to the lowest level
	//
	// Worst health with current erasure coding is 2 = (1 - (0-1)/1)
	rsc, _ := modules.NewRSCode(1, 1)
	fileSiaPath, err := subDir1_2.Join("test")
	if err != nil {
		t.Fatal(err)
	}
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     fileSiaPath,
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
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
	rt.renter.managedUpdateRenterContractsAndUtilities()
	offline, goodForRenew, _, _ := rt.renter.managedRenterContractsAndUtilities()
	fileHealth, _, _, _, _ := f.Health(offline, goodForRenew)
	if fileHealth != 2 {
		t.Fatalf("Expected heath to be 2, got %v", fileHealth)
	}

	// Mark the file as stuck by marking one of its chunks as stuck
	f.SetStuck(0, true)

	// Update the file metadata within the dir.
	err = rt.renter.managedUpdateFileMetadatas(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}

	// Now when we bubble the health and check for the worst health we should still see
	// that the health is the health of subDir1/subDir1 which was set to 1 again
	// and the stuck health will be the health of the stuck file
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	expectedMetadata, err = rt.renter.managedDirectoryMetadata(subDir1_1)
	if err != nil {
		t.Fatal(err)
	}
	expectedMetadata.AggregateHealth = 1
	expectedMetadata.AggregateStuckHealth = 2
	err = build.Retry(20, time.Second, func() error {
		// Get Root Directory Health
		metadata, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Compare the specific metadata fields we expected to be bubbled from
		// subDir1/subDir1 to root
		if metadata.AggregateHealth != expectedMetadata.AggregateHealth {
			return fmt.Errorf("AggregateHealth not equal; %v and %v", metadata.AggregateHealth, expectedMetadata.AggregateHealth)
		}
		if metadata.AggregateStuckHealth != expectedMetadata.AggregateStuckHealth {
			return fmt.Errorf("AggregateHealth not equal; %v and %v", metadata.AggregateStuckHealth, expectedMetadata.AggregateStuckHealth)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mark the file as un-stuck
	f.SetStuck(0, false)

	// Update the file metadata within the dir.
	err = rt.renter.managedUpdateFileMetadatas(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}

	// Now if we bubble the health and check for the worst health we should see
	// that the health is the health of the file
	beforeBubble = time.Now()
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	expectedHealth := siadir.Metadata{
		AggregateHealth:              2,
		AggregateLastHealthCheckTime: beforeBubble,
		AggregateModTime:             beforeBubble,
		AggregateNumFiles:            1,
		AggregateNumSubDirs:          8,
		AggregateSize:                100,
		AggregateStuckHealth:         0,
	}
	err = build.Retry(20, time.Second, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Health
		if err = equalBubbledMetadata(health, expectedHealth, time.Since(beforeBubble)); err != nil {
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
	if err := rt.renter.CreateDir(subDir1_2_1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	// Reset metadataUpdate with expected values
	beforeBubble = time.Now()
	expectedHealth = siadir.Metadata{
		AggregateHealth:              4,
		AggregateLastHealthCheckTime: beforeBubble,
		AggregateModTime:             beforeBubble,
	}
	if err := rt.openAndUpdateDir(subDir1_2_1, expectedHealth); err != nil {
		t.Fatal(err)
	}
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(20, time.Second, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryMetadata(modules.RootSiaPath())
		if err != nil {
			return err
		}
		// Check Health
		expectedHealth.AggregateNumFiles = 1
		expectedHealth.AggregateNumSubDirs = 9
		expectedHealth.AggregateSize = 100
		if err = equalBubbledMetadata(health, expectedHealth, time.Since(beforeBubble)); err != nil {
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

	// Create test renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a test directory with sub folders
	//
	// root/ 1
	// root/SubDir1/
	// root/SubDir1/SubDir1/
	// root/SubDir1/SubDir2/
	// root/SubDir2/
	// root/SubDir3/
	directories := []string{
		"root",
		"root/SubDir1",
		"root/SubDir1/SubDir1",
		"root/SubDir1/SubDir2",
		"root/SubDir2",
		"root/SubDir3",
	}

	// Create directory tree with consistent metadata
	now := time.Now()
	nowMD := siadir.Metadata{
		Health:                       1,
		StuckHealth:                  0,
		AggregateLastHealthCheckTime: now,
		LastHealthCheckTime:          now,
	}
	for _, dir := range directories {
		// Create Directory
		dirSiaPath := newSiaPath(dir)
		if err := rt.renter.CreateDir(dirSiaPath, modules.DefaultDirPerm); err != nil {
			t.Fatal(err)
		}
		err = rt.openAndUpdateDir(dirSiaPath, nowMD)
		if err != nil {
			t.Fatal(err)
		}
		// Put two files in the directory
		for i := 0; i < 2; i++ {
			fileSiaPath, err := dirSiaPath.Join(persist.RandomSuffix())
			if err != nil {
				t.Fatal(err)
			}
			sf, err := rt.renter.createRenterTestFile(fileSiaPath)
			if err != nil {
				t.Fatal(err)
			}
			sf.SetLastHealthCheckTime()
			err = errors.Compose(sf.SaveMetadata(), sf.Close())
			if err != nil {
				t.Fatal(err)
			}
		}
	}

	// Update all common directories to same health check time.
	err1 := rt.openAndUpdateDir(modules.RootSiaPath(), nowMD)
	err2 := rt.openAndUpdateDir(modules.BackupFolder, nowMD)
	err3 := rt.openAndUpdateDir(modules.HomeFolder, nowMD)
	err4 := rt.openAndUpdateDir(modules.SkynetFolder, nowMD)
	err5 := rt.openAndUpdateDir(modules.UserFolder, nowMD)
	err6 := rt.openAndUpdateDir(modules.VarFolder, nowMD)
	err = errors.Compose(err1, err2, err3, err4, err5, err6)
	if err != nil {
		t.Fatal(err)
	}

	// Set the LastHealthCheckTime of SubDir1/SubDir2 to be the oldest
	oldestCheckTime := now.AddDate(0, 0, -1)
	oldestHealthCheckUpdate := siadir.Metadata{
		Health:                       1,
		StuckHealth:                  0,
		AggregateLastHealthCheckTime: oldestCheckTime,
		LastHealthCheckTime:          oldestCheckTime,
	}
	subDir1_2 := newSiaPath("root/SubDir1/SubDir2")
	if err := rt.openAndUpdateDir(subDir1_2, oldestHealthCheckUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of SubDir1 so that the oldest LastHealthCheckTime of
	// SubDir1/SubDir2 gets bubbled up
	subDir1 := newSiaPath("root/SubDir1")
	err = rt.renter.managedBubbleMetadata(subDir1)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(60, time.Second, func() error {
		// Find the oldest directory, even though SubDir1/SubDir2 is the oldest,
		// SubDir1 should be returned since it is the lowest level directory tree
		// containing the Oldest LastHealthCheckTime
		dir, lastCheck, err := rt.renter.managedOldestHealthCheckTime()
		if err != nil {
			return err
		}
		if !dir.Equals(subDir1) {
			return fmt.Errorf("Expected to find %v but found %v", subDir1.String(), dir.String())
		}
		if !lastCheck.Equal(oldestCheckTime) {
			return fmt.Errorf("Expected to find time of %v but found %v", oldestCheckTime, lastCheck)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Now add more files to SubDir1, this will force the return of SubDir1_2
	// since the subtree including SubDir1 now has too many files
	for i := uint64(0); i < healthLoopNumBatchFiles; i++ {
		fileSiaPath, err := subDir1.Join(persist.RandomSuffix())
		if err != nil {
			t.Fatal(err)
		}
		sf, err := rt.renter.createRenterTestFile(fileSiaPath)
		if err != nil {
			t.Fatal(err)
		}
		sf.SetLastHealthCheckTime()
		err = errors.Compose(sf.SaveMetadata(), sf.Close())
		if err != nil {
			t.Fatal(err)
		}
	}
	err = rt.renter.managedBubbleMetadata(subDir1)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(60, time.Second, func() error {
		// Find the oldest directory, should be SubDir1_2 now
		dir, lastCheck, err := rt.renter.managedOldestHealthCheckTime()
		if err != nil {
			return err
		}
		if !dir.Equals(subDir1_2) {
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

	// Now update the root directory to have an older AggregateLastHealthCheckTime
	// than the sub directory but a more recent LastHealthCheckTime. This will
	// simulate a shutdown before all the pending bubbles could finish.
	entry, err := rt.renter.staticFileSystem.OpenSiaDir(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	rootTime := now.AddDate(0, 0, -3)
	err = entry.UpdateLastHealthCheckTime(rootTime, now)
	if err != nil {
		t.Fatal(err)
	}
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}

	// A call to managedOldestHealthCheckTime should still return the same
	// subDir1_2
	dir, lastCheck, err := rt.renter.managedOldestHealthCheckTime()
	if err != nil {
		t.Fatal(err)
	}
	if !dir.Equals(subDir1_2) {
		t.Error(fmt.Errorf("Expected to find %v but found %v", subDir1_2.String(), dir.String()))
	}
	if !lastCheck.Equal(oldestCheckTime) {
		t.Error(fmt.Errorf("Expected to find time of %v but found %v", oldestCheckTime, lastCheck))
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	// Add files
	rsc, _ := modules.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}

	// Call bubble on lowest lever and confirm top level reports accurate number
	// of files and aggregate number of files
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	// Add files
	rsc, _ := modules.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	fileSize := uint64(100)
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}

	// Call bubble on lowest lever and confirm top level reports accurate size
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	err = build.Retry(100, 100*time.Millisecond, func() error {
		dirInfo, err := rt.renter.staticFileSystem.DirInfo(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if dirInfo.AggregateSize != 2*fileSize {
			return fmt.Errorf("AggregateSize incorrect, got %v expected %v", dirInfo.AggregateSize, 2*fileSize)
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create directory tree
	subDir1, err := modules.NewSiaPath("SubDir1")
	if err != nil {
		t.Fatal(err)
	}
	subDir2, err := modules.NewSiaPath("SubDir2")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}

	// Call Bubble to update filesystem ModTimes so there are no zero times
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	// Sleep for 1 second to allow bubbles to update filesystem. Retry doesn't
	// work here as we are waiting for the ModTime to be fully updated but we
	// don't know what that value will be. We need this value to be updated and
	// static before we create the SiaFiles to be able to ensure the ModTimes of
	// the SiaFiles are the most recent
	time.Sleep(time.Second)

	// Add files
	sp1 := modules.RandomSiaPath()
	rsc, _ := modules.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     sp1,
		ErasureCode: rsc,
	}
	fileSize := uint64(100)
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	sp2, err := subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	up.SiaPath = sp2
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f1, err := rt.renter.staticFileSystem.OpenSiaFile(sp1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f1.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	f2, err := rt.renter.staticFileSystem.OpenSiaFile(sp2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := f2.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Call bubble on lowest lever and confirm top level reports accurate last
	// update time
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
	err = build.Retry(100, 100*time.Millisecond, func() error {
		dirInfo, err := rt.renter.staticFileSystem.DirInfo(modules.RootSiaPath())
		if err != nil {
			return err
		}
		if dirInfo.MostRecentModTime != f1.ModTime() {
			return fmt.Errorf("MostRecentModTime is incorrect, got %v expected %v", dirInfo.MostRecentModTime, f1.ModTime())
		}
		if dirInfo.AggregateMostRecentModTime != f2.ModTime() {
			return fmt.Errorf("AggregateMostRecentModTime is incorrect, got %v expected %v", dirInfo.AggregateMostRecentModTime, f2.ModTime())
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

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
	if err := rt.renter.CreateDir(subDir1, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}
	subDir1_2, err := subDir1.Join(subDir2.String())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir1_2, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}

	// Add a file to siafiles and SubDir1/SubDir2 and mark the first chunk as
	// stuck in each file
	//
	// This will test the edge case of continuing to find stuck files when a
	// directory has no files only directories
	rsc, _ := modules.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
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
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	up.SiaPath, err = subDir1_2.Join(hex.EncodeToString(fastrand.Bytes(8)))
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	f, err = rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
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
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	// Bubble directory information so NumStuckChunks is updated, there should
	// be at least 3 stuck chunks because of the 3 we manually marked as stuck,
	// but the repair loop could have marked the rest as stuck so we just want
	// to ensure that the root directory reflects at least the 3 we marked as
	// stuck
	err = rt.renter.managedBubbleMetadata(subDir1_2)
	if err != nil {
		t.Fatal(err)
	}
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
		if dir.Equals(modules.UserFolder) {
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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

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
	err = rt.renter.SetFileStuck(siaPath1, true)
	if err != nil {
		t.Fatal(err)
	}

	// File 2 will have only 1 chunk stuck
	file2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath2 := rt.renter.staticFileSystem.FileSiaPath(file2)
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

	// Since we disabled the health loop for this test, call it manually to
	// update the directory metadata
	err = rt.renter.managedBubbleMetadata(modules.UserFolder)
	if err != nil {
		t.Fatal(err)
	}
	i := 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
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
	checkFindRandomFile(t, rt.renter, modules.RootSiaPath(), siaPath1, siaPath2, siaPath3)

	// Create a directory
	dir, err := modules.NewSiaPath("Dir")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(dir, modules.DefaultDirPerm); err != nil {
		t.Fatal(err)
	}

	// Move siafiles to dir
	newSiaPath1, err := dir.Join(siaPath1.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, newSiaPath1)
	if err != nil {
		t.Fatal(err)
	}
	newSiaPath2, err := dir.Join(siaPath2.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath2, newSiaPath2)
	if err != nil {
		t.Fatal(err)
	}
	newSiaPath3, err := dir.Join(siaPath3.String())
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath3, newSiaPath3)
	if err != nil {
		t.Fatal(err)
	}
	// Since we disabled the health loop for this test, call it manually to
	// update the directory metadata
	err = rt.renter.managedBubbleMetadata(dir)
	if err != nil {
		t.Fatal(err)
	}
	i = 0
	err = build.Retry(100, 100*time.Millisecond, func() error {
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
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create a file
	rsc, _ := modules.NewRSCode(1, 1)
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
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), fileSize, persist.DefaultDiskPermissionsTest, up.DisablePartialChunk)
	if err != nil {
		t.Fatal(err)
	}
	sf, err := rt.renter.staticFileSystem.OpenSiaFile(up.SiaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Grab initial metadata values
	rt.renter.managedUpdateRenterContractsAndUtilities()
	offline, goodForRenew, _, _ := rt.renter.managedRenterContractsAndUtilities()
	health, stuckHealth, _, _, numStuckChunks := sf.Health(offline, goodForRenew)
	redundancy, _, err := sf.Redundancy(offline, goodForRenew)
	if err != nil {
		t.Fatal(err)
	}
	lastHealthCheckTime := sf.LastHealthCheckTime()
	modTime := sf.ModTime()

	// Update the file metadata.
	err = rt.renter.managedUpdateFileMetadatas(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	// Check calculated metadata
	bubbledMetadatas, err := rt.renter.managedCalculateFileMetadatas([]modules.SiaPath{up.SiaPath})
	if err != nil {
		t.Fatal(err)
	}
	fileMetadata := bubbledMetadatas[0].bm

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
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

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
	rsc, _ := modules.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     modules.RandomSiaPath(),
		ErasureCode: rsc,
	}
	err = rt.renter.staticFileSystem.NewSiaFile(up.SiaPath, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, persist.DefaultDiskPermissionsTest, false)
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
	rt.renter.staticWorkerPool.mu.Lock()
	for i := 0; i < int(f.NumChunks()); i++ {
		rt.renter.staticWorkerPool.workers[fmt.Sprint(i)] = &worker{
			killChan: make(chan struct{}),
			wakeChan: make(chan struct{}, 1),
		}
	}
	rt.renter.staticWorkerPool.mu.Unlock()

	// call managedAddStuckChunksToHeap, no chunks should be added
	err = rt.renter.managedAddStuckChunksToHeap(up.SiaPath, hosts, offline, goodForRenew)
	if !errors.Contains(err, errNoStuckChunks) {
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

// TestRandomStuckFileRegression tests an edge case where no siapath was being
// returned from managedStuckFile.
func TestRandomStuckFileRegression(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create Renter
	rt, err := newRenterTesterWithDependency(t.Name(), &dependencies.DependencyDisableRepairAndHealthLoops{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	// Create 1 file at root with all chunks stuck
	file, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := rt.renter.staticFileSystem.FileSiaPath(file)
	err = rt.renter.SetFileStuck(siaPath, true)
	if err != nil {
		t.Fatal(err)
	}

	// Set the root directories metadata to have a large number of aggregate
	// stuck chunks. Since there is only 1 stuck chunk this was causing the
	// likelihood of the stuck file being chosen to be very low.
	rootDir, err := rt.renter.staticFileSystem.OpenSiaDir(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	md, err := rootDir.Metadata()
	if err != nil {
		t.Fatal(err)
	}
	md.AggregateNumStuckChunks = 50000
	md.NumStuckChunks = 1
	md.NumFiles = 1
	err = rootDir.UpdateMetadata(md)
	if err != nil {
		t.Fatal(err)
	}

	stuckSiaPath, err := rt.renter.managedStuckFile(modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if !stuckSiaPath.Equals(siaPath) {
		t.Fatalf("Stuck siapath should have been the one file in the directory, expected %v got %v", siaPath, stuckSiaPath)
	}
}
