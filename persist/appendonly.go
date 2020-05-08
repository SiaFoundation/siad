package persist

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// LengthSize is the number of bytes set aside for the length on disk.
	LengthSize uint64 = 8

	// MetadataPageSize is the number of bytes set aside for the metadata page
	// on disk.
	MetadataPageSize uint64 = 4096
)

var (
	// ErrWrongHeader is the wrong header error.
	ErrWrongHeader = errors.New("wrong header")
	// ErrWrongVersion is the wrong version error.
	ErrWrongVersion = errors.New("wrong version")
)

// AppendOnlyPersist is the object responsible for creating, loading, and
// updating append-only persist files.
type AppendOnlyPersist struct {
	staticPath            string
	staticObjectSize      uint64
	staticMetadataHeader  types.Specifier
	staticMetadataVersion types.Specifier

	persistLength uint64
}

// NewAppendOnlyPersist creates a new AppendOnlyPersist object and initializes
// the persistence file.
func NewAppendOnlyPersist(dir, file string, size uint64, metadataHeader, metadataVersion types.Specifier) (*AppendOnlyPersist, io.ReadCloser, error) {
	aop := &AppendOnlyPersist{
		staticPath:            filepath.Join(dir, file),
		staticObjectSize:      size,
		staticMetadataHeader:  metadataHeader,
		staticMetadataVersion: metadataVersion,
	}
	r, err := aop.initPersist()
	return aop, r, err
}

// FilePath returns the filepath of the persist file.
func (aop *AppendOnlyPersist) FilePath() string {
	return aop.staticPath
}

// PersistLength returns the length of the persist data.
func (aop *AppendOnlyPersist) PersistLength() uint64 {
	return aop.persistLength
}

// Write implements the io.Writer interface. It updates the persist file,
// appending the changes to the persist file on disk.
func (aop *AppendOnlyPersist) Write(b []byte) (int, error) {
	filepath := aop.FilePath()
	// Truncate the file to remove any corrupted data that may have been added.
	err := os.Truncate(filepath, int64(aop.persistLength))
	if err != nil {
		return 0, err
	}
	// Open file
	f, err := os.OpenFile(filepath, os.O_RDWR, DefaultFilePermissions)
	if err != nil {
		return 0, errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Append data and sync
	numBytes, err := f.WriteAt(b, int64(aop.persistLength))
	if err != nil {
		return 0, errors.AddContext(err, "unable to append new data to blacklist persist file")
	}
	err = f.Sync()
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	aop.persistLength += uint64(numBytes)
	lengthBytes := encoding.Marshal(aop.persistLength)

	// Write to file
	lengthOffset := int64(2 * types.SpecifierLen)
	_, err = f.WriteAt(lengthBytes, lengthOffset)
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to write length")
	}
	err = f.Sync()
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to fsync file")
	}
	return numBytes, nil
}

// initPersist initializes the persistence file and returns the non-metadata
// bytes.
func (aop *AppendOnlyPersist) initPersist() (io.ReadCloser, error) {
	// Initialize the persistence directory
	err := os.MkdirAll(aop.staticPath, DefaultDirPermissions)
	if err != nil {
		return nil, errors.AddContext(err, "unable to make persistence directory")
	}

	// Try and load persistence.
	r, err := aop.load()
	if err == nil {
		// Return the loaded persistence bytes.
		return r, nil
	} else if !os.IsNotExist(err) {
		return nil, errors.AddContext(err, "unable to load persistence")
	}

	// Persist file doesn't exist, create it.
	f, err := os.OpenFile(aop.FilePath(), os.O_RDWR|os.O_CREATE, DefaultFilePermissions)
	if err != nil {
		return nil, errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Marshal the metadata.
	aop.persistLength = MetadataPageSize
	metadataBytes, err := aop.marshalMetadata()
	if err != nil {
		return nil, errors.AddContext(err, "unable to marshal metadata")
	}

	// Sanity check that the metadataBytes are less than the MetadataPageSize
	if uint64(len(metadataBytes)) > MetadataPageSize {
		err = fmt.Errorf("metadataBytes too long, %v > %v", len(metadataBytes), MetadataPageSize)
		build.Critical(err)
		return nil, err
	}

	// Write metadata to beginning of file. This is a small amount of data and
	// so operation is ACID as a single write and sync.
	_, err = f.WriteAt(metadataBytes, 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to write metadata to file on initialization")
	}
	err = f.Sync()
	if err != nil {
		return nil, errors.AddContext(err, "unable to fsync file")
	}

	// Return an empty reader since there were no persistence bytes.
	return nil, nil
}

// load loads the persist file from disk.
func (aop *AppendOnlyPersist) load() (io.ReadCloser, error) {
	// Open File
	filepath := aop.FilePath()
	f, err := os.Open(filepath)
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return nil, err
	}

	// Check the Header and Version of the file
	metadataSize := uint64(2*types.SpecifierLen) + LengthSize
	metadataBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(metadataBytes, 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read metadata bytes from file")
	}
	err = aop.unmarshalMetadata(metadataBytes)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal metadata bytes")
	}

	// Check if there are persisted objects after the metadata.
	goodBytes := aop.persistLength - MetadataPageSize
	if goodBytes <= 0 {
		return nil, nil
	}

	// Truncate the file to remove any corrupted data that may have been added.
	err = os.Truncate(filepath, int64(aop.persistLength))
	if err != nil {
		return nil, err
	}
	// Seek to the start of the persist file.
	_, err = f.Seek(int64(MetadataPageSize), 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to seek to start of persist file")
	}

	return f, nil
}

// marshalMetadata marshals the persist file's metadata and returns the byte
// slice.
func (aop *AppendOnlyPersist) marshalMetadata() ([]byte, error) {
	headerBytes, headerErr := aop.staticMetadataHeader.MarshalText()
	versionBytes, versionErr := aop.staticMetadataVersion.MarshalText()
	lengthBytes := encoding.Marshal(aop.persistLength)
	metadataBytes := append(headerBytes, append(versionBytes, lengthBytes...)...)
	return metadataBytes, errors.Compose(headerErr, versionErr)
}

// unmarshalMetadata ummarshals the persist file's metadata from the provided
// byte slice.
func (aop *AppendOnlyPersist) unmarshalMetadata(raw []byte) error {
	// Define offsets for reading from provided byte slice.
	versionOffset := types.SpecifierLen
	lengthOffset := 2 * types.SpecifierLen

	// Unmarshal and check header and version for correctness.
	var header, version types.Specifier
	err := header.UnmarshalText(raw[:versionOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal header")
	}
	if header != aop.staticMetadataHeader {
		return ErrWrongHeader
	}
	err = version.UnmarshalText(raw[versionOffset:lengthOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal version")
	}
	if version != aop.staticMetadataVersion {
		// Convert versions to strings and strip newlines for displaying.
		expected := string(bytes.Split(aop.staticMetadataVersion[:], []byte{'\n'})[0])
		received := string(bytes.Split(version[:], []byte{'\n'})[0])
		return errors.AddContext(ErrWrongVersion, fmt.Sprintf("expected %v, received %v", expected, received))
	}

	// Unmarshal the length
	return encoding.Unmarshal(raw[lengthOffset:], &aop.persistLength)
}
