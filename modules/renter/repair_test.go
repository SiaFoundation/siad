package renter

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// TODO - Adding testing for interruptions

// equalHealthsAndChunks is a helper that checks the Health, StuckHealth, and
// NumStuckChunks fields of two SiaDirHealths for equality. It does not look at
// the LastHealthCheckTime field
func equalHealthsAndChunks(health1, health2 siadir.SiaDirHealth) error {
	if health1.Health != health2.Health {
		return fmt.Errorf("Healths not equal, %v and %v", health1.Health, health2.Health)
	}
	if health1.StuckHealth != health2.StuckHealth {
		return fmt.Errorf("StuckHealths not equal, %v and %v", health1.StuckHealth, health2.StuckHealth)
	}
	if health1.NumStuckChunks != health2.NumStuckChunks {
		return fmt.Errorf("NumStuckChunks not equal, %v and %v", health1.NumStuckChunks, health2.NumStuckChunks)
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Check to make sure bubble doesn't error on an empty directory
	rt.renter.threadedBubbleHealth("")
	defaultHealth := siadir.SiaDirHealth{
		Health:              siadir.DefaultDirHealth,
		StuckHealth:         siadir.DefaultDirHealth,
		LastHealthCheckTime: time.Now(),
		NumStuckChunks:      0,
	}
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Check Health
		if err = equalHealthsAndChunks(health, defaultHealth); err != nil {
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
	subDir1 := "SubDir1"
	subDir2 := "SubDir2"
	if err := rt.renter.CreateDir(filepath.Join(subDir1, subDir1)); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(filepath.Join(subDir1, subDir2)); err != nil {
		t.Fatal(err)
	}

	// Set Healths of all the directories so they are not the defaults
	//
	// NOTE: You cannot set the NumStuckChunks to a non zero number without a
	// file in the directory as this will create a developer error
	siaPath := ""
	checkTime := time.Now()
	healthUpdate := siadir.SiaDirHealth{
		Health:              1,
		StuckHealth:         0,
		LastHealthCheckTime: checkTime,
	}
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, healthUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath = subDir1
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, healthUpdate); err != nil {
		t.Fatal(err)
	}
	siaPath = filepath.Join(subDir1, subDir1)
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, healthUpdate); err != nil {
		t.Fatal(err)
	}
	// Set health of subDir1/subDir2 to be the worst and set the
	siaPath = filepath.Join(subDir1, subDir2)
	healthUpdate.Health = 4
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, healthUpdate); err != nil {
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
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Compare to health of subDir1/subDir1
		expectedHealth, err := rt.renter.managedDirectoryHealth(filepath.Join(subDir1, subDir1))
		if err != nil {
			return err
		}
		if err = equalHealthsAndChunks(health, expectedHealth); err != nil {
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
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     filepath.Join(siaPath, "test"),
		ErasureCode: rsc,
	}
	f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777)
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
	offline, goodForRenew, _ := rt.renter.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{f})
	fileHealth, _, _ := f.Health(offline, goodForRenew)
	if fileHealth != 2 {
		t.Fatalf("Expected heath to be 2, got %v", fileHealth)
	}

	// Mark the file as stuck by marking one of its chunks as stuck
	f.SetStuck(0, true)

	// Now when we bubble the health and check for the worst health we should still see
	// that the health is the health of subDir1/subDir1 which was set to 1 again
	// and the stuck health will be the health of the stuck file
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Compare to health of subDir1/subDir1
		expectedHealth, err := rt.renter.managedDirectoryHealth(filepath.Join(subDir1, subDir1))
		if err != nil {
			return err
		}
		expectedHealth.StuckHealth = 2
		expectedHealth.NumStuckChunks++
		if err = equalHealthsAndChunks(health, expectedHealth); err != nil {
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
	rt.renter.threadedBubbleHealth(siaPath)
	expectedHealth := siadir.SiaDirHealth{
		Health:      2,
		StuckHealth: 0,
	}
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Check Health
		if err = equalHealthsAndChunks(health, expectedHealth); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update the RecentRepairTime of the file and confirm that the file's
	// health is now ignored
	if err := f.UpdateRecentRepairTime(); err != nil {
		t.Fatal(err)
	}
	expectedHealth.Health = 0
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Check Health
		if err = equalHealthsAndChunks(health, expectedHealth); err != nil {
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
	if err := rt.renter.CreateDir(filepath.Join(siaPath, subDir1)); err != nil {
		t.Fatal(err)
	}
	// Reset healthUpdate with expected values
	expectedHealth = siadir.SiaDirHealth{
		Health:              4,
		StuckHealth:         0,
		LastHealthCheckTime: time.Now(),
	}
	if err := rt.renter.staticDirSet.UpdateHealth(filepath.Join(siaPath, subDir1), expectedHealth); err != nil {
		t.Fatal(err)
	}
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Check Health
		if err = equalHealthsAndChunks(health, expectedHealth); err != nil {
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create directory tree
	subDir1 := "SubDir1"
	subDir2 := "SubDir2"
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2); err != nil {
		t.Fatal(err)
	}
	siaPath := filepath.Join(subDir1, subDir2)
	if err := rt.renter.CreateDir(siaPath); err != nil {
		t.Fatal(err)
	}

	// Set the LastHealthCheckTime of SubDir1/SubDir2 to be the oldest
	oldestCheckTime := time.Now().AddDate(0, 0, -1)
	oldestHealthCheckUpdate := siadir.SiaDirHealth{
		Health:              1,
		StuckHealth:         0,
		LastHealthCheckTime: oldestCheckTime,
	}
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, oldestHealthCheckUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of SubDir1 so that the oldest LastHealthCheckTime of
	// SubDir1/SubDir2 gets bubbled up
	go rt.renter.threadedBubbleHealth(subDir1)

	// Find the oldest directory, should be SubDir1/SubDir2
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, lastCheck, err := rt.renter.managedOldestHealthCheckTime()
		if err != nil {
			return err
		}
		if dir != siaPath {
			return fmt.Errorf("Expected to find %v but found %v", siaPath, dir)
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

// TestWorstHealthDirectory verifies that managedWorstHealthDirectory returns
// the correct directory
func TestWorstHealthDirectory(t *testing.T) {
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create directory tree
	subDir1 := "SubDir1"
	subDir2 := "SubDir2"
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2); err != nil {
		t.Fatal(err)
	}
	siaPath := filepath.Join(subDir1, subDir2)
	if err := rt.renter.CreateDir(siaPath); err != nil {
		t.Fatal(err)
	}

	// Confirm worst health directory is the top level directory since all
	// directories should be at the default health which is 0 or full health
	rt.renter.threadedBubbleHealth(subDir1)
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, health, err := rt.renter.managedWorstHealthDirectory()
		if err != nil {
			return err
		}
		if dir != "" {
			return fmt.Errorf("Expected to find top level directory but found %v", dir)
		}
		if health != 0 {
			return fmt.Errorf("Expected to find health of %v but found %v", 0, health)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set the Health of SubDir1/SubDir2 to be the worst
	worstHealth := float64(10)
	worstHealthUpdate := siadir.SiaDirHealth{
		Health:              worstHealth,
		StuckHealth:         0,
		LastHealthCheckTime: time.Now(),
	}
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, worstHealthUpdate); err != nil {
		t.Fatal(err)
	}

	// Bubble the health of SubDir1 so that the worst health of
	// SubDir1/SubDir2 gets bubbled up
	rt.renter.threadedBubbleHealth(subDir1)

	// Find the worst health directory, should be SubDir1/SubDir2
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, health, err := rt.renter.managedWorstHealthDirectory()
		if err != nil {
			return err
		}
		if dir != siaPath {
			return fmt.Errorf("Expected to find %v but found %v", siaPath, dir)
		}
		if health != worstHealth {
			return fmt.Errorf("Expected to find health of %v but found %v", worstHealth, health)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Add file to SubDir1/SubDir2
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     filepath.Join(siaPath, "test"),
		ErasureCode: rsc,
	}
	f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777)
	if err != nil {
		t.Fatal(err)
	}

	// Bubble health, confirm that the health worst directory is still
	// SubDir1/SubDir2
	rt.renter.threadedBubbleHealth(siaPath)

	// Worst health with current erasure coding is 2 = (1 - (0-1)/1)
	worstHealth = float64(2)
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, health, err := rt.renter.managedWorstHealthDirectory()
		if err != nil {
			return err
		}
		if dir != siaPath {
			return fmt.Errorf("Expected to find %v but found %v", siaPath, dir)
		}
		if health != worstHealth {
			return fmt.Errorf("Expected to find health of %v but found %v", worstHealth, health)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Update file's recent repair time
	if err := f.UpdateRecentRepairTime(); err != nil {
		t.Fatal(err)
	}

	// Bubble Health and confirm that the worst directory is now the top level
	// directory
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		dir, health, err := rt.renter.managedWorstHealthDirectory()
		if err != nil {
			return err
		}
		if dir != siaPath {
			return fmt.Errorf("Expected to find %v but found %v", siaPath, dir)
		}
		if health != float64(0) {
			return fmt.Errorf("Expected to find health of %v but found %v", float64(0), health)
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
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Create a test directory with sub folders
	//
	// root/
	// root/SubDir1/
	// root/SubDir1/SubDir2/
	// root/SubDir2/
	subDir1 := "SubDir1"
	subDir2 := "SubDir2"
	if err := rt.renter.CreateDir(subDir1); err != nil {
		t.Fatal(err)
	}
	if err := rt.renter.CreateDir(subDir2); err != nil {
		t.Fatal(err)
	}
	siaPath := filepath.Join(subDir1, subDir2)
	if err := rt.renter.CreateDir(siaPath); err != nil {
		t.Fatal(err)
	}

	// Add a file to root and SubDir1/SubDir2 and mark the first chunk as stuck
	// in each file
	//
	// This will test the edge case of continuing to find stuck files when a
	// directory has no files only directories
	rsc, _ := siafile.NewRSCode(1, 1)
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     "rootFile",
		ErasureCode: rsc,
	}
	f, err := rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	up.SiaPath = filepath.Join(siaPath, "subDir_1_subdir_2_file")
	f, err = rt.renter.staticFileSet.NewSiaFile(up, crypto.GenerateSiaKey(crypto.RandomCipherType()), 100, 0777)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.SetStuck(uint64(0), true); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}

	// Bubble directory information so NumStuckChunks is updated
	rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		// Get Root Directory Health
		health, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		// Check Health
		if health.NumStuckChunks != uint64(3) {
			return fmt.Errorf("Incorrect number of stuck chunks, got %v expected 3", health.NumStuckChunks)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Create map of stuck directories that contain files. These are the only
	// directories that should be returned.
	stuckDirectories := make(map[string]struct{})
	stuckDirectories[""] = struct{}{}
	stuckDirectories[siaPath] = struct{}{}

	// Find random directory several times, confirm that it finds a stuck
	// directory and there it finds unique directories
	var unique bool
	for i := 0; i < 10; i++ {
		dir, err := rt.renter.managedStuckDirectory()
		if err != nil {
			t.Fatal(err)
		}
		_, ok := stuckDirectories[dir]
		if !ok {
			t.Fatal("Found non stuck directory:", dir)
		}
		unique = true
	}
	if !unique {
		t.Fatal("No unique directories found")
	}
}
