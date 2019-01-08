package siatest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

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

// Delete removes the LocalFile from disk.
func (lf *LocalFile) Delete() error {
	return os.Remove(lf.path)
}

// Move moves the file to a new random location.
func (lf *LocalFile) Move() error {
	// Get the new path
	fileName := fmt.Sprintf("%dbytes %s", lf.size, hex.EncodeToString(fastrand.Bytes(4)))
	dir, _ := filepath.Split(lf.path)
	path := filepath.Join(dir, fileName)

	// Move the file
	if err := os.Rename(lf.path, path); err != nil {
		return err
	}
	lf.path = path
	return nil
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
