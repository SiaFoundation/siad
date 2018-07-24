package siatest

import (
	"fmt"
	"testing"
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

	// Create DIr
	levels := uint(3)
	ld, err := tn.CreateDirForTesting(levels)
	if err != nil {
		t.Fatal(err)
	}

	if err := ld.checkSubDir(int(levels), int(levels)); err != nil {
		t.Fatal(err)
	}
}

func (ld *LocalDir) checkSubDir(remaining, levels int) error {
	// Check for correct number of files
	if len(ld.Files) != levels {
		return fmt.Errorf("Did not get expected number of files, got %v expected %v", len(ld.Files), levels)
	}

	// Check for correct number of sub directories
	if remaining == 0 {
		return nil
	}
	if len(ld.SubDir) != levels {
		return fmt.Errorf("Did not get expected number of subdirs, got %v expected %v", len(ld.SubDir), levels)
	}
	for i := 0; i < levels; i++ {
		if err := ld.SubDir[i].checkSubDir(remaining-1, levels); err != nil {
			return err
		}
	}
	return nil
}
