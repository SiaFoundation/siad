package siatest

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/fastrand"
)

var (
	// SiaTestingDir is the directory that contains all of the files and
	// folders created during testing.
	SiaTestingDir = filepath.Join(os.TempDir(), "SiaTesting")
)

// LocalDir is a helper struct that represents a directory on disk that is to be
// uploaded to the sia network
type LocalDir struct {
	path   string
	SubDir []*LocalDir
	Files  []*LocalFile
}

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

// filesDir returns the path to the files directory of the TestNode. The files
// directory is where new files are stored before being uploaded.
func (tn *TestNode) filesDir() string {
	path := filepath.Join(tn.Dir, "files")
	if err := os.MkdirAll(path, 0777); err != nil {
		panic(err)
	}
	return path
}

// downloadsDir returns the path to the download directory of the TestNode.
func (tn *TestNode) downloadsDir() string {
	path := filepath.Join(tn.Dir, "downloads")
	if err := os.MkdirAll(path, 0777); err != nil {
		panic(err)
	}
	return path
}

// NewLocalDir creates a LocalDir.  The `levels` parameter indicates how my
// levels the directory has as well as how many subdirectories and files are in
// each level of the directory
func (tn *TestNode) NewLocalDir(levels uint) (*LocalDir, error) {
	// Create Dir for uploading
	ld := &LocalDir{
		path: filepath.Join(tn.filesDir(), fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4)))),
	}
	// Create all directory
	if err := os.MkdirAll(ld.path, 0777); err != nil {
		return nil, err
	}
	for i := 0; i < int(levels); i++ {
		ld.SubDir = append(ld.SubDir, &LocalDir{
			path: filepath.Join(ld.path, fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4)))),
		})
		if err := tn.createSubDir(ld.SubDir[i], levels-1, levels); err != nil {
			return nil, err
		}
		lf, err := tn.NewFile(100+Fuzz(), ld.path)
		if err != nil {
			return nil, err
		}
		ld.Files = append(ld.Files, lf)
	}
	return ld, nil
}

// createSubDir creates the remaining sub directories needed for testing
func (tn *TestNode) createSubDir(ld *LocalDir, remaining, levels uint) error {
	// Create reminging sub directories
	if remaining > 0 {
		for i := 0; i < int(levels); i++ {
			ld.SubDir = append(ld.SubDir, &LocalDir{
				path: filepath.Join(ld.path, fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4)))),
			})
			if err := tn.createSubDir(ld.SubDir[i], remaining-1, levels); err != nil {
				return err
			}
		}
	}

	// Create all directories to this level
	if err := os.MkdirAll(ld.path, 0777); err != nil {
		return err
	}

	// Create Files in directory
	for i := 0; i < int(levels); i++ {
		lf, err := tn.NewFile(100+Fuzz(), ld.path)
		if err != nil {
			return err
		}
		ld.Files = append(ld.Files, lf)
	}
	return nil
}

// dirName returns the directory name of the directory on disk
func (ld *LocalDir) dirName() string {
	return filepath.Base(ld.path)
}
