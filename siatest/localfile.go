package siatest

import (
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

type (
	// LocalFile is a helper struct that represents a file uploaded to the Sia
	// network.
	LocalFile struct {
		path     string
		size     int
		checksum crypto.Hash
	}
)

// NewLocalFile creates and returns a new LocalFile. It
// will write size random bytes to the file and give the file a random name.
func NewLocalFile(size int, dir string) (*LocalFile, error) {
	fileName := fmt.Sprintf("%dbytes-%s", size, hex.EncodeToString(fastrand.Bytes(4)))
	path := filepath.Join(dir, fileName)
	bytes := fastrand.Bytes(size)
	err := ioutil.WriteFile(path, bytes, 0600)
	return &LocalFile{
		path:     path,
		size:     size,
		checksum: crypto.HashBytes(bytes),
	}, err
}

// NewFile creates and returns a new LocalFile. The file will be created in the
// TestNode's upload directory
func (tn *TestNode) NewFile(size int) (*LocalFile, error) {
	return NewLocalFile(size, tn.uploadDir.path)
}

// LocalFileSiaPath returns the siapath of the file on disk
func (tn *TestNode) LocalFileSiaPath(lf *LocalFile) string {
	return strings.TrimPrefix(lf.path, tn.RenterDir()+"/")
}

// Delete removes the LocalFile from disk.
func (lf *LocalFile) Delete() error {
	return os.Remove(lf.path)
}

// checkIntegrity compares the in-memory checksum to the checksum of the data
// on disk
func (lf *LocalFile) checkIntegrity() error {
	data, err := ioutil.ReadFile(lf.path)
	if lf.size == 0 {
		data = fastrand.Bytes(lf.size)
	}
	if err != nil {
		return errors.AddContext(err, "failed to read file from disk")
	}
	if crypto.HashBytes(data) != lf.checksum {
		return errors.New("checksums don't match")
	}
	return nil
}

// FileName returns the file name of the file on disk
func (lf *LocalFile) FileName() string {
	return filepath.Base(lf.path)
}

// partialChecksum returns the checksum of a part of the file.
func (lf *LocalFile) partialChecksum(from, to uint64) (crypto.Hash, error) {
	data, err := ioutil.ReadFile(lf.path)
	if err != nil {
		return crypto.Hash{}, errors.AddContext(err, "failed to read file from disk")
	}
	return crypto.HashBytes(data[from:to]), nil
}
