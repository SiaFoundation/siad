package renter

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// TODO - Adding testing for interruptions

// TestBubbleHealth tests to make sure that the health of the most in need file
// in a directory is bubbled up to the right levels and probes the supporting
// functions as well
//
// TODO - this test should be expanded to check that the health is bubbled
// correctly when a file is stuck. Code to mark files/chunks as stuck is still
// needed
func TestBubbleHealth(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	// Create test renter
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}

	// Check to make sure bubble doesn't error on an empty directory
	go rt.renter.threadedBubbleHealth("")
	build.Retry(100, 100*time.Millisecond, func() error {
		health, _, _, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		if health != 0 {
			return fmt.Errorf("Expected health to be %v, got %v", 0, health)
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
	siaPath := ""
	checkTime := time.Now()
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, 1, 0, checkTime); err != nil {
		t.Fatal(err)
	}
	siaPath = subDir1
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, 1, 0, checkTime); err != nil {
		t.Fatal(err)
	}
	siaPath = filepath.Join(subDir1, subDir1)
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, 1, 0, checkTime); err != nil {
		t.Fatal(err)
	}
	siaPath = filepath.Join(subDir1, subDir2)
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, 4, 0, checkTime); err != nil {
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
	go rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		health, _, _, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		if health != 1 {
			return fmt.Errorf("Expected health to be %v, got %v", 1, health)
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
	offline, _, _ := rt.renter.managedRenterContractsAndUtilities([]*siafile.SiaFileSetEntry{f})
	health, _ := f.Health(offline)
	if health != 2 {
		t.Fatalf("Expected heath to be 2, got %v", health)
	}
	go rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		health, _, _, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		if health != 2 {
			return fmt.Errorf("Expected health to be %v, got %v", 2, health)
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
	if err := rt.renter.staticDirSet.UpdateHealth(filepath.Join(siaPath, subDir1), 4, 0, checkTime); err != nil {
		t.Fatal(err)
	}
	go rt.renter.threadedBubbleHealth(siaPath)
	build.Retry(100, 100*time.Millisecond, func() error {
		health, _, _, err := rt.renter.managedDirectoryHealth("")
		if err != nil {
			return err
		}
		if health != 4 {
			return fmt.Errorf("Expected health to be %v, got %v", 4, health)
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
	if err := rt.renter.staticDirSet.UpdateHealth(siaPath, 1, 0, oldestCheckTime); err != nil {
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
