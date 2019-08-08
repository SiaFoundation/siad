package siafile

import (
	"bytes"
	"encoding/hex"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siadir"
)

// newTestSiaFileSetWithFile creates a new SiaFileSet and SiaFile and makes sure
// that they are linked
func newTestSiaFileSetWithFile() (*SiaFileSetEntry, *SiaFileSet, error) {
	// Create new SiaFile params
	_, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams(1, true)
	dir := filepath.Join(os.TempDir(), "siafiles", hex.EncodeToString(fastrand.Bytes(16)))
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

// TestAddExistingSiafile tests the AddExistingSiaFile method's behavior.
func TestAddExistingSiafile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()
	// Create a fileset with file.
	sf, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}
	// Add the existing file to the set again this shouldn't do anything.
	if err := sfs.AddExistingSiaFile(sf.SiaFile, []chunk{}); err != nil {
		t.Fatal(err)
	}
	numSiaFiles := 0
	err = filepath.Walk(sfs.staticSiaFileDir, func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == modules.SiaFileExtension {
			numSiaFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// There should be 1 siafile.
	if numSiaFiles != 1 {
		t.Fatalf("Found %v siafiles but expected %v", numSiaFiles, 1)
	}
	// Load the same siafile again, but change the UID.
	b, err := ioutil.ReadFile(sf.siaFilePath)
	if err != nil {
		t.Fatal(err)
	}
	reader := bytes.NewReader(b)
	newSF, newChunks, err := LoadSiaFileFromReaderWithChunks(reader, sf.SiaFilePath(), sf.wal)
	if err != nil {
		t.Fatal(err)
	}
	// Grab the pre-import UID after changing it.
	newSF.UpdateUniqueID()
	preImportUID := newSF.UID()
	// Import the file. This should work because the files no longer share the same
	// UID.
	if err := sfs.AddExistingSiaFile(newSF, newChunks); err != nil {
		t.Fatal(err)
	}
	// sf and newSF should have the same pieces.
	for chunkIndex := uint64(0); chunkIndex < sf.NumChunks(); chunkIndex++ {
		piecesOld, err1 := sf.Pieces(chunkIndex)
		piecesNew, err2 := newSF.Pieces(chunkIndex)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(piecesOld, piecesNew) {
			t.Log("piecesOld: ", piecesOld)
			t.Log("piecesNew: ", piecesNew)
			t.Fatal("old pieces don't match new pieces")
		}
	}
	numSiaFiles = 0
	err = filepath.Walk(sfs.staticSiaFileDir, func(path string, info os.FileInfo, err error) error {
		if filepath.Ext(path) == modules.SiaFileExtension {
			numSiaFiles++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// There should be 2 siafiles.
	if numSiaFiles != 2 {
		t.Fatalf("Found %v siafiles but expected %v", numSiaFiles, 2)
	}
	// The UID should have changed.
	if newSF.UID() == preImportUID {
		t.Fatal("newSF UID should have changed after importing the file")
	}
	if !strings.HasSuffix(newSF.SiaFilePath(), "_1"+modules.SiaFileExtension) {
		t.Fatal("SiaFile should have a suffix but didn't")
	}
	// Should be able to open the new file from disk.
	if _, err := os.Stat(newSF.SiaFilePath()); err != nil {
		t.Fatal(err)
	}
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
	_, siaPath, source, rc, sk, fileSize, _, fileMode := newTestFileParams(1, true)
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
		// The set should be empty except for the partials file.
		if len(sfs.siaFileMap) != 1 {
			t.Fatal("SiaFileMap should have 1 file")
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

	// Confirm 2 files are in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatalf("Expected SiaFileSet map to be of length 2, instead is length %v", len(sfs.siaFileMap))
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
	if len(sfs.siaFileMap) != 1 {
		t.Fatalf("Expected SiaFileSet map to contain 1 file, instead is length %v", len(sfs.siaFileMap))
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
	// Confirm there are 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close File
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is one file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}

	// Test accessing the same file from two separate threads
	//
	// Open file
	entry1, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is 2 file in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// Access the file again
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close one of the file instances
	err = entry1.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}

	// Confirm closing out remaining files removes all files from memory
	//
	// Close last instance of the first file
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is one file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
}

// TestRenameFileInMemory confirms that threads that have access to a file
// will continue to have access to the file even it another thread renames it
func TestRenameFileInMemory(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create SiaFileSet with SiaFile and corresponding combined siafile.
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

	// Confirm there are 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected  file in memory, got:", len(sfs.siaFileMap))
	}

	// Test renaming an instance of a file
	//
	// Access file with another instance
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm that renter still only has 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 file in memory, got:", len(sfs.siaFileMap))
	}
	_, err = os.Stat(entry.SiaFilePath())
	if err != nil {
		println("err2", err.Error())
	}
	// Rename second instance
	newSiaPath := modules.RandomSiaPath()
	err = sfs.Rename(siaPath, newSiaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are still only 2 files in memory as renaming doesn't add
	// the new name to memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close instance of renamed file
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there are still only 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// Close other instance of second file
	err = entry.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is only 1 file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
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

	// Confirm there are 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}

	// Test deleting an instance of a file
	//
	// Access the file again
	entry2, err := sfs.Open(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	// Confirm there is still only has 2 files in memory
	if len(sfs.siaFileMap) != 2 {
		t.Fatal("Expected 2 files in memory, got:", len(sfs.siaFileMap))
	}
	// delete and close instance of file
	if err := sfs.Delete(siaPath); err != nil {
		t.Fatal(err)
	}
	err = entry2.Close()
	if err != nil {
		t.Fatal(err)
	}
	// There should be one file in the set after deleting it.
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
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
	// Confirm renter has one file in memory
	if len(sfs.siaFileMap) != 1 {
		t.Fatal("Expected 1 file in memory, got:", len(sfs.siaFileMap))
	}
}

// TestDeleteCorruptSiaFile confirms that the siafileset will delete a siafile
// even if it cannot be opened
func TestDeleteCorruptSiaFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	t.Parallel()

	// Create siafileset
	_, sfs, err := newTestSiaFileSetWithFile()
	if err != nil {
		t.Fatal(err)
	}

	// Create siafile on disk with random bytes
	siaPath, err := modules.NewSiaPath("badFile")
	if err != nil {
		t.Fatal(err)
	}
	siaFilePath := siaPath.SiaFileSysPath(sfs.staticSiaFileDir)
	err = ioutil.WriteFile(siaFilePath, fastrand.Bytes(100), 0666)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the siafile cannot be opened
	_, err = sfs.Open(siaPath)
	if err == nil || err == ErrUnknownPath {
		t.Fatal("expected open to fail for read error but instead got:", err)
	}

	// Delete the siafile
	err = sfs.Delete(siaPath)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm the file is no longer on disk
	_, err = os.Stat(siaFilePath)
	if !os.IsNotExist(err) {
		t.Fatal("Expected err to be that file does not exists but was:", err)
	}
}

// TestSiaDirDelete tests the DeleteDir method of the siafileset.
func TestSiaDirDelete(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a siadirset
	dirRoot := filepath.Join(os.TempDir(), "siadirs", t.Name())
	os.RemoveAll(dirRoot)
	os.RemoveAll(dirRoot)
	wal, _ := newTestWAL()
	sds := siadir.NewSiaDirSet(dirRoot, wal)
	sfs := NewSiaFileSet(dirRoot, wal)

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
	// Specify a function that's executed in parallel which continuously saves a
	// file to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *SiaFileSetEntry) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.SaveHeader()
			if err != nil && !strings.Contains(err.Error(), "can't call createAndApplyTransaction on deleted file") {
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
		// 50% chance to close the dir.
		if fastrand.Intn(2) == 0 {
			entry.Close()
		}
		// Create a file in the dir.
		fileSP, err := sp.Join(hex.EncodeToString(fastrand.Bytes(16)))
		if err != nil {
			t.Fatal(err)
		}
		_, _, source, rc, sk, fileSize, _, fileMode := newTestFileParams(1, true)
		sf, err := sfs.NewSiaFile(modules.FileUploadParams{Source: source, SiaPath: fileSP, ErasureCode: rc}, sk, fileSize, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(sf)
		} else {
			sf.Close()
		}
	}
	// Wait a second for the goroutines to write to disk a few times.
	time.Sleep(time.Second)
	// Delete dir1.
	sp, err := modules.NewSiaPath("dir1")
	if err != nil {
		t.Fatal(err)
	}
	if err := sfs.DeleteDir(sp, sds.Delete); err != nil {
		t.Fatal(err)
	}

	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// The root siafile dir should be empty except for 1 .siadir file and a .csia
	// file.
	files, err := ioutil.ReadDir(sfs.staticSiaFileDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		for _, file := range files {
			t.Log("Found ", file.Name())
		}
		t.Fatalf("There should be %v files/folders in the root dir but found %v\n", 1, len(files))
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != modules.SiaDirExtension &&
			filepath.Ext(file.Name()) != modules.PartialsSiaFileExtension {
			t.Fatal("Encountered unexpected file:", file.Name())
		}
	}
}

// TestSiaDirRename tests the RenameDir method of the siafileset.
func TestSiaDirRename(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	// Prepare a siadirset
	dirRoot := filepath.Join(os.TempDir(), "siadirs", t.Name())
	os.RemoveAll(dirRoot)
	os.RemoveAll(dirRoot)
	wal, _ := newTestWAL()
	sds := siadir.NewSiaDirSet(dirRoot, wal)
	sfs := NewSiaFileSet(dirRoot, wal)

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
	// Specify a function that's executed in parallel which continuously saves a
	// file to disk.
	stop := make(chan struct{})
	wg := new(sync.WaitGroup)
	f := func(entry *SiaFileSetEntry) {
		defer wg.Done()
		defer entry.Close()
		for {
			select {
			case <-stop:
				return
			default:
			}
			err := entry.SaveHeader()
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
		// 50% chance to close the dir.
		if fastrand.Intn(2) == 0 {
			entry.Close()
		}
		// Create a file in the dir.
		fileSP, err := sp.Join(hex.EncodeToString(fastrand.Bytes(16)))
		if err != nil {
			t.Fatal(err)
		}
		_, _, source, rc, sk, fileSize, _, fileMode := newTestFileParams(1, true)
		sf, err := sfs.NewSiaFile(modules.FileUploadParams{Source: source, SiaPath: fileSP, ErasureCode: rc}, sk, fileSize, fileMode)
		if err != nil {
			t.Fatal(err)
		}
		// 50% chance to spawn goroutine. It's not realistic to assume that all dirs
		// are loaded.
		if fastrand.Intn(2) == 0 {
			wg.Add(1)
			go f(sf)
		} else {
			sf.Close()
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
	if err := sfs.RenameDir(oldPath, newPath, sds.Rename); err != nil {
		t.Fatal(err)
	}
	// Wait another second for more writes to disk after renaming the dir before
	// killing the goroutines.
	time.Sleep(time.Second)
	close(stop)
	wg.Wait()
	time.Sleep(time.Second)
	// Make sure we can't open any of the old folders/files on disk but we can open
	// the new ones.
	for _, dir := range dirStructure {
		oldDir, err1 := modules.NewSiaPath(dir)
		newDir, err2 := oldDir.Rebase(oldPath, newPath)
		if err := errors.Compose(err1, err2); err != nil {
			t.Fatal(err)
		}
		// Open entry with old dir. Shouldn't work.
		_, err := sds.Open(oldDir)
		if err != siadir.ErrUnknownPath {
			t.Fatal("shouldn't be able to open old path", oldDir.String(), err)
		}
		// Old dir shouldn't exist.
		if _, err = os.Stat(oldDir.SiaDirSysPath(dirRoot)); !os.IsNotExist(err) {
			t.Fatal(err)
		}
		// Open entry with new dir. Should succeed.
		entry, err := sds.Open(newDir)
		if err != nil {
			t.Fatal(err)
		}
		defer entry.Close()
		// New dir should contain 1 siafile.
		fis, err := ioutil.ReadDir(newDir.SiaDirSysPath(dirRoot))
		if err != nil {
			t.Fatal(err)
		}
		numFiles := 0
		for _, fi := range fis {
			if !fi.IsDir() && filepath.Ext(fi.Name()) == modules.SiaFileExtension {
				numFiles++
			}
		}
		if numFiles != 1 {
			t.Fatalf("there should be 1 file in the new dir not %v", numFiles)
		}
		// New entry should have a file.
		// Check siapath of entry.
		if entry.SiaPath() != newDir {
			t.Fatalf("entry should have siapath '%v' but was '%v'", newDir, entry.SiaPath())
		}
	}
}
