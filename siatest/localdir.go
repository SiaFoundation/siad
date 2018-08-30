package siatest

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/fastrand"
)

// LocalDir is a helper struct that represents a directory on disk that is to be
// uploaded to the sia network
type LocalDir struct {
	path string
}

// Name returns the directory name of the directory on disk
func (ld *LocalDir) Name() string {
	return filepath.Base(ld.path)
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

// CreateDir creates a new LocalDir in the current LocalDir with the provide
// name
func (ld *LocalDir) CreateDir(name string) (*LocalDir, error) {
	path := filepath.Join(ld.path, name)
	return &LocalDir{path: path}, os.MkdirAll(path, 0777)
}

// newDir creates a new LocalDir in the current LocalDir
func (ld *LocalDir) newDir() (*LocalDir, error) {
	path := filepath.Join(ld.path, fmt.Sprintf("dir-%s", hex.EncodeToString(fastrand.Bytes(4))))
	return &LocalDir{path: path}, os.MkdirAll(path, 0777)
}

// NewFile creates a new LocalFile in the current LocalDir
func (ld *LocalDir) NewFile(size int) (*LocalFile, error) {
	fileName := fmt.Sprintf("%dbytes-%s", size, hex.EncodeToString(fastrand.Bytes(4)))
	path := filepath.Join(ld.path, fileName)
	bytes := fastrand.Bytes(size)
	err := ioutil.WriteFile(path, bytes, 0600)
	return &LocalFile{
		path:     path,
		size:     size,
		checksum: crypto.HashBytes(bytes),
	}, err
}

// Path creates a new LocalFile in the current LocalDir
func (ld *LocalDir) Path() string {
	return ld.path
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

// subDirs returns a slice of the sub directories in the LocalDir
func (ld *LocalDir) subDirs() ([]*LocalDir, error) {
	var dirs []*LocalDir
	fileInfos, err := ioutil.ReadDir(ld.path)
	if err != nil {
		return dirs, err
	}
	for _, f := range fileInfos {
		if f.IsDir() {
			dirs = append(dirs, &LocalDir{
				path: filepath.Join(ld.path, f.Name()),
			})
		}
	}
	return dirs, nil
}
