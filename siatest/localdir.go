package siatest

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/fastrand"
)

// TODO: Add ability to get sub directories and files in a LocalDir

// LocalDir is a helper struct that represents a directory on disk that is to be
// uploaded to the sia network
type LocalDir struct {
	path string
}

// NewLocalDir creates and returns a new LocalDir.
func (tn *TestNode) NewLocalDir() (*LocalDir, error) {
	dirPath := filepath.Join(tn.filesDir(), fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4))))
	err := os.MkdirAll(dirPath, 0777)
	return &LocalDir{
		path: dirPath,
	}, err
}

// newDir creates a new LocalDir in the current LocalDir
func (ld *LocalDir) newDir() (*LocalDir, error) {
	path := filepath.Join(ld.path, fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4))))
	if err := os.MkdirAll(path, 0777); err != nil {
		return nil, err
	}
	return &LocalDir{path: path}, nil
}

// newFile creates a new LocalFile in the current LocalDir
func (ld *LocalDir) newFile(size int) (*LocalFile, error) {
	return newLocalFile(size, ld.path)
}

// PopulateDir populates a LocalDir levels deep with the number of files and
// directories provided at each level
func (ld *LocalDir) PopulateDir(files, dirs, levels uint) error {
	// Check for end level
	if levels == 0 {
		return nil
	}

	// Create files at current level
	for i := 0; i < int(files); i++ {
		_, err := ld.newFile(100 + Fuzz())
		if err != nil {
			return err
		}
	}

	// Create directories at current level
	for i := 0; i < int(dirs); i++ {
		subld, err := ld.newDir()
		if err != nil {
			return err
		}
		if err = subld.PopulateDir(files, dirs, levels-1); err != nil {
			return err
		}
	}
	return nil
}
