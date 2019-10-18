package siadir

import (
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// newTestSiaDirSet creates a new SiaDirSet
func newTestSiaDirSet() *SiaDirSet {
	// Create params
	dir := filepath.Join(os.TempDir(), "siadirs")
	wal, _ := newTestWAL()
	return NewSiaDirSet(dir, wal)
}

// newTestSiaDirSetWithDir creates a new SiaDirSet and SiaDir and makes sure
// that they are linked
func newTestSiaDirSetWithDir() (*SiaDirSetEntry, *SiaDirSet, error) {
	// Create directory
	dir := filepath.Join(os.TempDir(), "siadirs")
	// Create SiaDirSet and SiaDirSetEntry
	wal, _ := newTestWAL()
	sds := NewSiaDirSet(dir, wal)
	entry, err := sds.NewSiaDir(modules.RandomSiaPath())
	if err != nil {
		return nil, nil, err
	}
	return entry, sds, nil
}

// TestInitRootDir checks that InitRootDir creates a siadir on disk and that it
// can be called again without returning an error
func TestInitRootDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create new SiaDirSet
	sds := newTestSiaDirSet()

	// Create a root SiaDirt
	if err := sds.InitRootDir(); err != nil {
		t.Fatal(err)
	}

	// Verify the siadir exists on disk
	siaPath := modules.RootSiaPath().SiaDirMetadataSysPath(sds.staticRootDir)
	_, err := os.Stat(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Verify that the siadir is not stored in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("SiaDirSet has siadirs in memory")
	}

	// Try initializing the root directory again, there should be no error
	if err := sds.InitRootDir(); err != nil {
		t.Fatal(err)
	}
}
