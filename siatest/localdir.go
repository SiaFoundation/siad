package siatest

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
)

// LocalDir is a helper struct that represents a directory on disk that is to be
// uploaded to the sia network
type LocalDir struct {
	path string
}

// NewLocalDir creates a new LocalDir
func (tn *TestNode) NewLocalDir() (*LocalDir, error) {
	fileName := fmt.Sprintf("dir-%s", persist.RandomSuffix())
	path := filepath.Join(tn.RenterDir(), fileName)
	err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest)
	if err != nil {
		return nil, errors.AddContext(err, "unable to make local directory for new local dir")
	}
	return &LocalDir{
		path: path,
	}, nil
}

// CreateDir creates a new LocalDir in the current LocalDir with the provided
// name.
func (ld *LocalDir) CreateDir(name string) (*LocalDir, error) {
	path := filepath.Join(ld.path, name)
	err := os.MkdirAll(path, persist.DefaultDiskPermissionsTest)
	if err != nil {
		return nil, errors.AddContext(err, "unable to os.MkdirAll a directory")
	}
	return &LocalDir{
		path: path,
	}, nil
}

// Files returns a slice of the files in the LocalDir
func (ld *LocalDir) Files() ([]*LocalFile, error) {
	var files []*LocalFile
	fileInfos, err := ioutil.ReadDir(ld.path)
	if err != nil {
		return files, err
	}
	for _, f := range fileInfos {
		if f.IsDir() {
			continue
		}
		size := int(f.Size())
		bytes := fastrand.Bytes(size)
		files = append(files, &LocalFile{
			path:     filepath.Join(ld.path, f.Name()),
			size:     size,
			checksum: crypto.HashBytes(bytes),
		})
	}
	return files, nil
}

// Name returns the directory name of the directory on disk
func (ld *LocalDir) Name() string {
	return filepath.Base(ld.path)
}

// NewFile creates a new LocalFile in the current LocalDir
func (ld *LocalDir) NewFile(size int) (*LocalFile, error) {
	fileName := fmt.Sprintf("%dbytes - %s", size, persist.RandomSuffix())
	return ld.newFile(size, fileName, 0600)
}

// NewFileWithName creates a new LocalFile in the current LocalDir with the
// given name and size.
func (ld *LocalDir) NewFileWithName(name string, size int) (*LocalFile, error) {
	return ld.newFile(size, name, 0600)
}

// NewFileWithMode creates a new LocalFile in the current LocalDir with the
// given mode and size.
func (ld *LocalDir) NewFileWithMode(size int, mode os.FileMode) (*LocalFile, error) {
	fileName := fmt.Sprintf("%dbytes - %s", size, persist.RandomSuffix())
	return ld.newFile(size, fileName, mode)
}

// Path creates a new LocalFile in the current LocalDir
func (ld *LocalDir) Path() string {
	return ld.path
}

// PopulateDir populates a LocalDir levels deep with the number of files and
// directories provided at each level. The same number of files and directories
// will be at each level
func (ld *LocalDir) PopulateDir(files, dirs, levels uint) error {
	// Check for end level
	if levels == 0 {
		return nil
	}

	// Create files at current level
	for i := 0; i < int(files); i++ {
		_, err := ld.NewFile(100 + Fuzz())
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

// newDir creates a new LocalDir in the current LocalDir
func (ld *LocalDir) newDir() (*LocalDir, error) {
	path := filepath.Join(ld.path, fmt.Sprintf("dir-%s", persist.RandomSuffix()))
	return &LocalDir{path: path}, os.MkdirAll(path, persist.DefaultDiskPermissionsTest)
}

// newFile creates a new LocalFile in the current LocalDir with the given size,
// name and mode.
func (ld *LocalDir) newFile(size int, name string, mode os.FileMode) (*LocalFile, error) {
	path := filepath.Join(ld.path, name)
	bytes := fastrand.Bytes(size)
	err := ioutil.WriteFile(path, bytes, mode)
	return &LocalFile{
		path:     path,
		size:     size,
		checksum: crypto.HashBytes(bytes),
	}, err
}
