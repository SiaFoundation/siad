package siatest

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/persist"
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

// partialChecksum returns the checksum of a part of the file.
func (lf *LocalFile) partialChecksum(from, to uint64) (crypto.Hash, error) {
	data, err := ioutil.ReadFile(lf.path)
	if err != nil {
		return crypto.Hash{}, errors.AddContext(err, "failed to read file from disk")
	}
	return crypto.HashBytes(data[from:to]), nil
}

// Data will return the data of the file, so that it can be compared against
// output such as download output after it has been deleted locally.
func (lf *LocalFile) Data() ([]byte, error) {
	localData, err := ioutil.ReadFile(lf.path)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read local file data")
	}
	return localData, nil
}

// Delete removes the LocalFile from disk.
func (lf *LocalFile) Delete() error {
	return os.Remove(lf.path)
}

// Equal will compare the input to the bytes of the local file, returning an
// error if the bytes are not a perfect match, or if there is an error reading
// the local file data.
func (lf *LocalFile) Equal(data []byte) error {
	localData, err := ioutil.ReadFile(lf.path)
	if err != nil {
		return errors.AddContext(err, "unable to read local file data")
	}
	if bytes.Compare(data, localData) != 0 {
		return errors.New("local file data does not match input data")
	}
	return nil
}

// FileName returns the file name of the file on disk
func (lf *LocalFile) FileName() string {
	return filepath.Base(lf.path)
}

// Move moves the file to a new random location.
func (lf *LocalFile) Move() error {
	// Get the new path
	fileName := fmt.Sprintf("%dbytes %s", lf.size, persist.RandomSuffix())
	dir, _ := filepath.Split(lf.path)
	path := filepath.Join(dir, fileName)

	// Move the file
	if err := os.Rename(lf.path, path); err != nil {
		return err
	}
	lf.path = path
	return nil
}

// Path returns the on-disk path of the local file.
func (lf *LocalFile) Path() string {
	return lf.path
}
