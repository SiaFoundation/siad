package siafile

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// newRandSiaPath creates a new SiaPath type with a random path.
func newRandSiaPath() modules.SiaPath {
	siaPath, err := modules.NewSiaPath(hex.EncodeToString(fastrand.Bytes(4)))
	if err != nil {
		panic(err)
	}
	return siaPath
}

// newTestSiaFileSetWithFile creates a new SiaFileSet and SiaFile and makes sure
// that they are linked
func newTestSiaFileSetWithFile() (*SiaFileSetEntry, *SiaFileSet, error) {
	// Create new SiaFile params
	_, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams()
	dir := filepath.Join(os.TempDir(), "siafiles")
	// Create SiaFileSet
	wal, _ := newTestWAL()
	sfs := NewSiaFileSet(dir, wal)
	// Create SiaFile
	up := modules.FileUploadParams{
		Source:      source,
		SiaPath:     siaPath,
		ErasureCode: rc,
	}
	entry, err := sfs.NewSiaFile(up, sk, fileSize, fileMode)
	if err != nil {
		return nil, nil, err
	}
	return entry, sfs, nil
}

// TestSiaFileSetDeleteOpen checks that deleting an entry from the set followed
// by creating a Siafile with the same name without closing the deleted entry
// works as expected.
func TestSiaFileSetDeleteOpen(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create new SiaFile params
	_, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams()
	// Create SiaFileSet
	wal, _ := newTestWAL()
	dir := filepath.Join(os.TempDir(), "siafiles")
	sfs := NewSiaFileSet(dir, wal)

	// Repeatedly create a SiaFile and delete it while still keeping the entry
	// around. That should only be possible without errors if the correctly
	// delete the entry from the set.
	var entries []*SiaFileSetEntry
	for i := 0; i < 10; i++ {
		// Create SiaFile
		up := modules.FileUploadParams{
			Source:      source,
			SiaPath:     siaPath,
			ErasureCode: rc,
		}
		entry, err := sfs.NewSiaFile(up, sk, fileSize, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		// Delete SiaFile
		if err := sfs.Delete(sfs.SiaPath(entry)); err != nil {
			t.Fatal(err)
		}
		// The set should be empty.
		if len(sfs.siaFileMap) != 0 {
			t.Fatal("SiaFileMap should be empty")
		}
		// Append the entry to make sure we can close it later.
		entries = append(entries, entry)
	}
	// The SiaFile shouldn't exist anymore.
	exists := sfs.Exists(siaPath)
	if exists {
		t.Fatal("SiaFile shouldn't exist anymore")
	}
	// Close the entries.
	for _, entry := range entries {
		if err := entry.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestSiaFileSetOpenClose tests that the threadCount of the siafile is
// incremented and decremented properly when Open() and Close() are called
func TestSiaFileSetOpenClose(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.SiaPath(entry)
	exists := sfs.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm file is in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatalf("Expected SiaFileSet map to be of length 1, instead is length %v", len(sfs.siaFileMap))
	}

	// Confirm threadCount is incremented properly
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadMap to be of length 1, got %v", len(entry.threadMap))
	}

	// Close SiaFileSetEntry
	entry.Close()

	// Confirm that threadCount was decremented
	if len(entry.threadMap) != 0 {
		t.Fatalf("Expected threadCount to be 0, got %v", len(entry.threadMap))
	}

	// Confirm file was removed from memory
	if len(sfs.siaFileMap) != 0 {
		t.Fatalf("Expected SiaFileSet map to be empty, instead is length %v", len(sfs.siaFileMap))
	}

	// Open siafile again and confirm threadCount was incremented
	entry, err = sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry.threadMap) != 1 {
		t.Fatalf("Expected threadCount to be 1, got %v", len(entry.threadMap))
	}
}

// TestFilesInMemory confirms that files are added and removed from memory
// as expected when files are in use and not in use
func TestFilesInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.SiaPath(entry)
	exists := sfs.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
	// Close File
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm therte are no files in memory
	if len(sfs.siaFileMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.siaFileMap))
	}

	// Test accessing the same file from two separate threads
	//
	// Open file
	entry1, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
	// Access the file again
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
	// Close one of the file instances
	err = entry1.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first file
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are no files in memory
	if len(sfs.siaFileMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.siaFileMap))
	}
}

// TestRenameFileInMemory confirms that threads that have access to a file
// will continue to have access to the file even it another thread renames it
func TestRenameFileInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.SiaPath(entry)
	exists := sfs.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm there is 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}

	// Test renaming an instance of a file
	//
	// Access file with another instance
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that renter still only has 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
	// Rename second instance
	newSiaPath := newRandSiaPath()
	err = sfs.Rename(siaPath, newSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory as renaming doesn't
	// add the new name to memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close instance of renamed file
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still has 1 file1 in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close other instance of second file
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are no files in memory
	if len(sfs.siaFileMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.siaFileMap))
	}
}

// TestDeleteFileInMemory confirms that threads that have access to a file
// will continue to have access to the file even it another thread deletes it
func TestDeleteFileInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile
	entry, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath := sfs.SiaPath(entry)
	exists := sfs.Exists(siaPath)
	if !exists {
		t.Fatal("No SiaFileSetEntry found")
	}
	if err != nil {
		t.Fatal(err)
	}

	// Confirm there is 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}

	// Test deleting an instance of a file
	//
	// Access the file again
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
	// delete and close instance of file
	if err := sfs.Delete(siaPath); err != nil {
		t.Fatal(err)
	}
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// There should be no more file in the set after deleting it.
	if len(sfs.siaFileMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.siaFileMap))
	}
	// confirm other instance is still in memory by calling methods on it
	if !entry.Deleted() {
		t.Fatal("Expected file to be deleted")
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first file
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm renter has no files in memory
	if len(sfs.siaFileMap) != 0 {
		t.Fatal("Expected 0 files in memory, got:", len(sfs.siaFileMap))
	}
}
