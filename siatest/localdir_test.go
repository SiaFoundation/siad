package siatest

import (
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/errors"
)

// TestCreateDir tests the creation of a new directory
func TestCreateDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Specify the parameters for the group
	groupParams := GroupParams{
		Miners: 1,
	}
	// Create the group
	tg, err := NewGroupFromTemplate(siatestTestDir(t.Name()), groupParams)
	if err != nil {
		t.Fatal("Failed to create group: ", err)
	}
	defer func() {
		if err := tg.Close(); err != nil {
			t.Fatal(err)
		}
	}()

	tn := tg.Miners()[0]

	// Create Directory
	files := uint(3)
	dirs := uint(3)
	levels := uint(3)
	ld, err := tn.uploadDir.newDir()
	if err != nil {
		t.Fatal(err)
	}
	if err = ld.PopulateDir(files, dirs, levels); err != nil {
		t.Fatal(err)
	}

	// Check directory creation
	if err := checkSubDir(ld.path, int(files), int(dirs), int(levels)); err != nil {
		t.Fatal(err)
	}
}

// checkSubDir is a helper function that confirms sub directories are created as
// expected
func checkSubDir(path string, files, dirs, levels int) error {
	// Check for last level
	if levels == 0 {
		return nil
	}

	// Read directory
	dirFiles, err := ioutil.ReadDir(path)
	if err != nil {
		return errors.AddContext(err, "could not read directory")
	}

	// Count files and directories in current directory
	numFiles := 0
	numDirs := 0
	for _, f := range dirFiles {
		if f.IsDir() {
			numDirs++
			if err = checkSubDir(filepath.Join(path, f.Name()), files, dirs, levels-1); err != nil {
				return err
			}
			continue
		}
		numFiles++
	}
	if numFiles != files {
		return fmt.Errorf("Did not find expected number of files, found %v expected %v", numFiles, files)
	}
	if numDirs != dirs {
		return fmt.Errorf("Did not find expected number of directories, found %v expected %v", numDirs, dirs)
	}

	return nil
}
