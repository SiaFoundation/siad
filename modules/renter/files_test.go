package renter

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/renter/filesystem"
	"gitlab.com/NebulousLabs/Sia/modules/renter/siafile"
)

// newTestingWal is a helper method to create a wal during testing.
func newTestingWal() *writeaheadlog.WAL {
	walDir := filepath.Join(os.TempDir(), "wals")
	if err := os.MkdirAll(walDir, 0700); err != nil {
		panic(err)
	}
	walPath := filepath.Join(walDir, hex.EncodeToString(fastrand.Bytes(8)))
	_, wal, err := writeaheadlog.New(walPath)
	if err != nil {
		panic(err)
	}
	return wal
}

// newRenterTestFile creates a test file when the test has a renter so that the
// file is properly added to the renter. It returns the SiaFileSetEntry that the
// SiaFile is stored in
func (r *Renter) newRenterTestFile() (*filesystem.FileNode, error) {
	// Generate name and erasure coding
	siaPath, rsc := testingFileParams()
	// create the renter/files dir if it doesn't exist
	siaFilePath := r.staticFileSystem.FilePath(siaPath)
	dir, _ := filepath.Split(siaFilePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	// Create File
	up := modules.FileUploadParams{
		Source:      "",
		SiaPath:     siaPath,
		ErasureCode: rsc,
	}
	// Prepend the path with the siafiles folder as the renter normally would.
	sp, err := modules.UserSiaPath().Join(up.SiaPath.String())
	if err != nil {
		return nil, err
	}
	err = r.staticFileSystem.NewSiaFile(sp, up.Source, up.ErasureCode, crypto.GenerateSiaKey(crypto.RandomCipherType()), 1000, 0777, false)
	if err != nil {
		return nil, err
	}
	return r.staticFileSystem.OpenSiaFile(sp)
}

// TestRenterFileListLocalPath verifies that FileList() returns the correct
// local path information for an uploaded file.
func TestRenterFileListLocalPath(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	id := rt.renter.mu.Lock()
	entry, _ := rt.renter.newRenterTestFile()
	if err := entry.SetLocalPath("TestPath"); err != nil {
		t.Fatal(err)
	}
	rt.renter.mu.Unlock(id)
	files, err := rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("wrong number of files, got", len(files), "wanted one")
	}
	if files[0].LocalPath != "TestPath" {
		t.Fatal("file had wrong LocalPath: got", files[0].LocalPath, "wanted TestPath")
	}
}

// TestRenterDeleteFile probes the DeleteFile method of the renter type.
func TestRenterDeleteFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Delete a file from an empty renter.
	siaPath, err := modules.NewSiaPath("dne")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.DeleteFile(siaPath)
	if err != filesystem.ErrNotExist {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}

	// Put a file in the renter.
	entry, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	// Delete a different file.
	siaPathOne, err := modules.NewSiaPath("one")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.DeleteFile(siaPathOne)
	if err != filesystem.ErrNotExist {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}
	// Delete the file.
	siapath := rt.renter.staticFileSystem.FileSiaPath(entry)
	siapath, err = siapath.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}

	entry.Close()
	err = rt.renter.DeleteFile(siapath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Error("file was deleted, but is still reported in FileList")
	}
	// Confirm that file was removed from SiaFileSet
	_, err = rt.renter.staticFileSystem.OpenSiaFile(siapath)
	if err == nil {
		t.Fatal("Deleted file still found in staticFileSet")
	}

	// Put a file in the renter, then rename it.
	entry2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	siaPath1, err := modules.NewSiaPath("1")
	if err != nil {
		t.Fatal(err)
	}
	siapath2 := rt.renter.staticFileSystem.FileSiaPath(entry2)
	siapath2, err = siapath2.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	err = rt.renter.RenameFile(siapath2, siaPath1) // set name to "1"
	if err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	entry2.Close()
	siapath2 = rt.renter.staticFileSystem.FileSiaPath(entry2)
	siapath2, err = siapath2.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	err = rt.renter.RenameFile(siapath2, siaPathOne)
	if err != nil {
		t.Fatal(err)
	}
	// Call delete on the previous name.
	err = rt.renter.DeleteFile(siaPath1)
	if err != filesystem.ErrNotExist {
		t.Errorf("Expected '%v' got '%v'", siafile.ErrUnknownPath, err)
	}
	// Call delete on the new name.
	err = rt.renter.DeleteFile(siaPathOne)
	if err != nil {
		t.Error(err)
	}

	// Check that all .sia files have been deleted.
	var walkStr string
	rt.renter.staticFileSystem.Walk(modules.RootSiaPath(), func(path string, _ os.FileInfo, _ error) error {
		// capture only .sia files
		if filepath.Ext(path) == ".sia" {
			rel, _ := filepath.Rel(rt.renter.staticFileSystem.Root(), path) // strip testdir prefix
			walkStr += rel
		}
		return nil
	})
	expWalkStr := ""
	if walkStr != expWalkStr {
		t.Fatalf("Bad walk string: expected %q, got %q", expWalkStr, walkStr)
	}
}

// TestRenterFileList probes the FileList method of the renter type.
func TestRenterFileList(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Get the file list of an empty renter.
	files, err := rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatal("FileList has non-zero length for empty renter?")
	}

	// Put a file in the renter.
	entry1, _ := rt.renter.newRenterTestFile()
	files, err = rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("FileList is not returning the only file in the renter")
	}
	entry1SP := rt.renter.staticFileSystem.FileSiaPath(entry1)
	entry1SP, err = entry1SP.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	if !files[0].SiaPath.Equals(entry1SP) {
		t.Error("FileList is not returning the correct filename for the only file")
	}

	// Put multiple files in the renter.
	entry2, _ := rt.renter.newRenterTestFile()
	entry2SP := rt.renter.staticFileSystem.FileSiaPath(entry2)
	entry2SP, err = entry2SP.Rebase(modules.UserSiaPath(), modules.RootSiaPath())
	if err != nil {
		t.Fatal(err)
	}
	files, err = rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("Expected %v files, got %v", 2, len(files))
	}
	files, err = rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !((files[0].SiaPath.Equals(entry1SP) || files[0].SiaPath.Equals(entry2SP)) &&
		(files[1].SiaPath.Equals(entry1SP) || files[1].SiaPath.Equals(entry2SP)) &&
		(files[0].SiaPath != files[1].SiaPath)) {
		t.Log("files[0].SiaPath", files[0].SiaPath)
		t.Log("files[1].SiaPath", files[1].SiaPath)
		t.Log("file1.SiaPath()", rt.renter.staticFileSystem.FileSiaPath(entry1).String())
		t.Log("file2.SiaPath()", rt.renter.staticFileSystem.FileSiaPath(entry2).String())
		t.Error("FileList is returning wrong names for the files")
	}
}

// TestRenterRenameFile probes the rename method of the renter.
func TestRenterRenameFile(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Rename a file that doesn't exist.
	siaPath1, err := modules.NewSiaPath("1")
	if err != nil {
		t.Fatal(err)
	}
	siaPath1a, err := modules.NewSiaPath("1a")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err.Error() != filesystem.ErrNotExist.Error() {
		t.Errorf("Expected '%v' got '%v'", filesystem.ErrNotExist, err)
	}

	// Get the filesystem.
	sfs := rt.renter.staticFileSystem

	// Rename a file that does exist.
	entry, _ := rt.renter.newRenterTestFile()
	var sp modules.SiaPath
	if err := sp.FromSysPath(entry.SiaFilePath(), sfs.DirPath(modules.UserSiaPath())); err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(sp, siaPath1)
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err != nil {
		t.Fatal(err)
	}
	files, err := rt.renter.FileList(modules.RootSiaPath(), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatal("FileList has unexpected number of files:", len(files))
	}
	if !files[0].SiaPath.Equals(siaPath1a) {
		t.Errorf("RenameFile failed: expected %v, got %v", siaPath1a.String(), files[0].SiaPath)
	}
	// Confirm SiaFileSet was updated
	sp1a, err := modules.UserSiaPath().Join(siaPath1a.String())
	if err != nil {
		t.Fatal(err)
	}
	sp1, err := modules.UserSiaPath().Join(siaPath1.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = rt.renter.staticFileSystem.OpenSiaFile(sp1a)
	if err != nil {
		t.Fatal("renter staticFileSet not updated to new file name:", err)
	}
	_, err = rt.renter.staticFileSystem.OpenSiaFile(sp1)
	if err == nil {
		t.Fatal("old name not removed from renter staticFileSet")
	}
	// Rename a file to an existing name.
	entry2, err := rt.renter.newRenterTestFile()
	if err != nil {
		t.Fatal(err)
	}
	var sp2 modules.SiaPath
	if err := sp2.FromSysPath(entry2.SiaFilePath(), sfs.DirPath(modules.UserSiaPath())); err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(sp2, siaPath1) // Rename to "1"
	if err != nil {
		t.Fatal(err)
	}
	entry2.Close()
	err = rt.renter.RenameFile(siaPath1, siaPath1a)
	if err != filesystem.ErrExists {
		t.Fatal("Expecting ErrExists, got", err)
	}
	// Rename a file to the same name.
	err = rt.renter.RenameFile(siaPath1, siaPath1)
	if err != filesystem.ErrExists {
		t.Fatal("Expecting ErrExists, got", err)
	}

	// Confirm ability to rename file
	siaPath1b, err := modules.NewSiaPath("1b")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1, siaPath1b)
	if err != nil {
		t.Fatal(err)
	}
	// Rename file that would create a directory
	siaPathWithDir, err := modules.NewSiaPath("new/name/with/dir/test")
	if err != nil {
		t.Fatal(err)
	}
	err = rt.renter.RenameFile(siaPath1b, siaPathWithDir)
	if err != nil {
		t.Fatal(err)
	}

	// Confirm directory metadatas exist
	dirSiaPath, err := modules.UserSiaPath().Join(siaPathWithDir.String())
	if err != nil {
		t.Fatal(err)
	}
	for !dirSiaPath.Equals(modules.RootSiaPath()) {
		dirSiaPath, err = dirSiaPath.Dir()
		if err != nil {
			t.Fatal(err)
		}
		_, err = rt.renter.staticFileSystem.OpenSiaDir(dirSiaPath)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestRenterFileDir tests that the renter files are uploaded to the files
// directory and not the root directory of the renter.
func TestRenterFileDir(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}
	rt, err := newRenterTester(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	// Create local file to upload
	localDir := filepath.Join(rt.dir, "files")
	if err := os.MkdirAll(localDir, 0700); err != nil {
		t.Fatal(err)
	}
	size := 100
	fileName := fmt.Sprintf("%dbytes %s", size, hex.EncodeToString(fastrand.Bytes(4)))
	source := filepath.Join(localDir, fileName)
	bytes := fastrand.Bytes(size)
	if err := ioutil.WriteFile(source, bytes, 0600); err != nil {
		t.Fatal(err)
	}

	// Upload local file
	ec, err := siafile.NewRSCode(DefaultDataPieces, DefaultParityPieces)
	if err != nil {
		t.Fatal(err)
	}
	siaPath, err := modules.NewSiaPath(fileName)
	if err != nil {
		t.Fatal(err)
	}
	params := modules.FileUploadParams{
		Source:      source,
		SiaPath:     siaPath,
		ErasureCode: ec,
	}
	err = rt.renter.Upload(params)
	if err != nil {
		t.Fatal("failed to upload file:", err)
	}

	// Get file and check siapath
	f, err := rt.renter.File(siaPath)
	if err != nil {
		t.Fatal(err)
	}
	if !f.SiaPath.Equals(siaPath) {
		t.Fatalf("siapath not set as expected: got %v expected %v", f.SiaPath, fileName)
	}

	// Confirm .sia file exists on disk in the SiapathRoot directory
	renterDir := filepath.Join(rt.dir, modules.RenterDir)
	siapathRootDir := filepath.Join(renterDir, modules.FileSystemRoot, modules.HomeFolderRoot, modules.UserRoot)
	fullPath := siaPath.SiaFileSysPath(siapathRootDir)
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		t.Fatal("No .sia file found on disk")
	}
}
