package siatest

import (
	"os"
	"path/filepath"
)

var (
	// SiaTestingDir is the directory that contains all of the files and
	// folders created during testing.
	SiaTestingDir = filepath.Join(os.TempDir(), "SiaTesting")
)

// TestDir joins the provided directories and prefixes them with the Sia
// testing directory, removing any files or directories that previously existed
// at that location.
func TestDir(dirs ...string) string {
	path := filepath.Join(SiaTestingDir, "siatest", filepath.Join(dirs...))
	err := os.RemoveAll(path)
	if err != nil {
		panic(err)
	}
	return path
}

// siatestTestDir creates a testing directory for tests within the siatest
// module.
func siatestTestDir(testName string) string {
	path := TestDir("siatest", testName)
	if err := os.MkdirAll(path, 0777); err != nil {
		panic(err)
	}
	return path
}

// DownloadDir returns the LocalDir that is the testnodes download directory
func (tn *TestNode) DownloadDir() *LocalDir {
	return tn.downloadDir
}

// RenterDir returns the renter directory for the renter
func (tn *TestNode) RenterDir() string {
	return filepath.Join(tn.Dir, "renter")
}

// RenterFilesDir returns the renter's files directory
func (tn *TestNode) RenterFilesDir() string {
	return filepath.Join(tn.RenterDir(), "siafiles")
}

// FilesDir returns the LocalDir that is the testnodes upload directory
func (tn *TestNode) FilesDir() *LocalDir {
	return tn.filesDir
}
