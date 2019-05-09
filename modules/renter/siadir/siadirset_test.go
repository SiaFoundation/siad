package siadir

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

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
	siaPath := modules.RootSiaPath().SiaDirMetadataSysPath(sds.rootDir)
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

// TestSiaDirSetOpenClose tests that the threadCount of the siadir is
// incremented and decremented properly when Open() and Close() are called
func TestSiaDirSetOpenClose(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm dir is in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatalf("Expected SiaDirSet map to be of length 1, instead is length %v", len(sds.siaDirMap))
	}

	// Confirm threadCount is incremented properly
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(entry.threadMap))
	}

	// Close SiaDirSetEntry
	entry.Close()

	// Confirm that threadCount was decremented
	if len(entry.threadMap) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(entry.threadMap))
	}

	// Confirm dir was removed from memory
	if len(sds.siaDirMap) != 0 {
		t.Fatalf("Expected SiaDirSet map to be empty, instead is length %v", len(sds.siaDirMap))
	}

	// Open siafile again and confirm threadCount was incremented
	entry, err = sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(entry.threadMap))
	}
}

// TestDirsInMemory confirms that files are added and removed from memory
// as expected when files are in use and not in use
func TestDirsInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Close File
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm therte are no files in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sds.siaDirMap))
	}

	// Test accessing the same dir from two separate threads
	//
	// Open dir
	entry1, err := sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Access the dir again
	entry2, err := sds.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}
	// Close one of the dir instances
	err = entry1.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 dir in memory
	if len(sds.siaDirMap) != 1 {
		t.Fatal("Expected 1 dir in memory, got:", len(sds.siaDirMap))
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first dir
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are no files in memory
	if len(sds.siaDirMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sds.siaDirMap))
	}
}

// TestUpdateSiaDirSetMetadata probes the UpdateMetadata method of the SiaDirSet
func TestUpdateSiaDirSetMetadata(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaDirSet with SiaDir
	entry, sds, err := newTestSiaDirSetWithDir()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := entry.SiaPath()
	exists, err := sds.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaDirSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm metadata is set properly
	md := entry.metadata
	if err = checkMetadataInit(md); err != nil {
		t.Fatal(err)
	}

	// Update the metadata of the entry
	checkTime := time.Now()
	metadataUpdate := md
	// Aggregate fields
	metadataUpdate.AggregateHealth = 7
	metadataUpdate.AggregateLastHealthCheckTime = checkTime
	metadataUpdate.AggregateMinRedundancy = 2.2
	metadataUpdate.AggregateModTime = checkTime
	metadataUpdate.AggregateNumFiles = 11
	metadataUpdate.AggregateNumStuckChunks = 15
	metadataUpdate.AggregateNumSubDirs = 5
	metadataUpdate.AggregateSize = 2432
	metadataUpdate.AggregateStuckHealth = 5
	// SiaDir fields
	metadataUpdate.Health = 4
	metadataUpdate.LastHealthCheckTime = checkTime
	metadataUpdate.MinRedundancy = 2
	metadataUpdate.ModTime = checkTime
	metadataUpdate.NumFiles = 5
	metadataUpdate.NumStuckChunks = 6
	metadataUpdate.NumSubDirs = 4
	metadataUpdate.Size = 223
	metadataUpdate.StuckHealth = 2

	err = sds.UpdateMetadata(siaPath, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}

	// Check if the metadata was updated properly in memory and on disk
	md = entry.metadata
	err = equalMetadatas(md, metadataUpdate)
	if err != nil {
		t.Fatal(err)
	}
}

// TestSiaDirRename tests the Rename method of the siadirset.
func TestSiaDirRename(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a siadirset
	dir := filepath.Join(os.TempDir(), "siadirs", t.Name())
	os.RemoveAll(dir)
	wal, _ := newTestWAL()
	sds := NewSiaDirSet(dir, wal)

	// Specify a directory structure for this test.
	var dirStructure = []string{
		"dir1",
		"dir1/subdir1",
		"dir1/subdir1/subsubdir1",
		"dir1/subdir1/subsubdir2",
		"dir1/subdir1/subsubdir3",
		"dir1/subdir2",
		"dir1/subdir2/subsubdir1",
		"dir1/subdir2/subsubdir2",
		"dir1/subdir2/subsubdir3",
		"dir1/subdir3",
		"dir1/subdir3/subsubdir1",
		"dir1/subdir3/subsubdir2",
		"dir1/subdir3/subsubdir3",
	}
	// Specify a function that's executed in parallel which continuously saves dirs
	// to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *SiaDirSetEntry) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.UpdateMetadata(Metadata{})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	// Create the structure and spawn a goroutine that keeps saving the structure
	// to disk for each directory.
	for _, dir := range dirStructure {
		sp, err := modules.NewSiaPath(dir)
		if err != nil {
			t.Fatal(err)
		}
		entry, err := sds.NewSiaDir(sp)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(entry)
		} else {
			entry.Close()
		}
	}
	// Wait a second for the goroutines to write to disk a few times.
	time.Sleep(time.Second)
	// Rename dir1 to dir2.
	oldPath, err1 := modules.NewSiaPath(dirStructure[0])
	newPath, err2 := modules.NewSiaPath("dir2")
	if err := errors.Compose(err1, err2); err != nil {
		t.Fatal(err)
	}
	if err := sds.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// Make sure we can't open any of the old folders on disk but we can open the
	// new ones.
	for _, dir := range dirStructure {
		oldDir, err1 := modules.NewSiaPath(dir)
		newDir, err2 := oldDir.Rebase(oldPath, newPath)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		// Open entry with old dir. Shouldn't work.
		_, err := sds.Open(oldDir)
		if err != ErrUnknownPath {
			t.Fatal("shouldn't be able to open old path", oldDir.String(), err)
		}
		// Open entry with new dir. Should succeed.
		entry, err := sds.Open(newDir)
		if err != nil {
			t.Fatal(err)
		}
		defer entry.Close()
		// Check siapath of entry.
		if entry.siaPath != newDir {
			t.Fatalf("entry should have siapath '%v' but was '%v'", newDir, entry.siaPath)
		}
	}
}
