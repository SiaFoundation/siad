package persist

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/types"
)

const (
	// MetadataPageSize is the number of bytes set aside for the metadata page
	// on disk. It is the length of a disk sector so that we can ensure that a
	// metadata disk write is ACID with one write and sync and we don't have to
	// worry about the persist data ever crossing the first disk sector.
	MetadataPageSize uint64 = 4096

	// lengthSize is the number of bytes set aside for storing the length of the
	// data persisted on disk.
	lengthSize uint64 = 8
)

var (
	// ErrWrongHeader is the wrong header error.
	ErrWrongHeader = errors.New("wrong header")
	// ErrWrongVersion is the wrong version error.
	ErrWrongVersion = errors.New("wrong version")
)

type (
	// AppendOnlyPersist is the object responsible for creating, loading, and
	// updating append-only persist files.
	AppendOnlyPersist struct {
		staticPath string
		staticF    *os.File

		metadata appendOnlyPersistMetadata

		mu sync.Mutex
	}

	// appendOnlyPersistMetadata contains metadata for the AppendOnlyPersist
	// file.
	appendOnlyPersistMetadata struct {
		Header  types.Specifier
		Version types.Specifier

		Length uint64
	}
)

// NewAppendOnlyPersist creates a new AppendOnlyPersist object and initializes
// the persistence file.
func NewAppendOnlyPersist(dir, file string, metadataHeader, metadataVersion types.Specifier) (*AppendOnlyPersist, io.Reader, error) {
	aop := &AppendOnlyPersist{
		staticPath: filepath.Join(dir, file),
		metadata: appendOnlyPersistMetadata{
			Header:  metadataHeader,
			Version: metadataVersion,
		},
	}
	reader, err := aop.initOrLoadPersist(dir)
	return aop, reader, err
}

// Close closes the persist file, freeing the resource and preventing further
// writes.
func (aop *AppendOnlyPersist) Close() error {
	return aop.staticF.Close()
}

// FilePath returns the filepath of the persist file.
func (aop *AppendOnlyPersist) FilePath() string {
	return aop.staticPath
}

// PersistLength returns the length of the persist data.
func (aop *AppendOnlyPersist) PersistLength() uint64 {
	aop.mu.Lock()
	defer aop.mu.Unlock()

	return aop.metadata.Length
}

// Write implements the io.Writer interface. It updates the persist file,
// appending the changes to the persist file on disk.
func (aop *AppendOnlyPersist) Write(b []byte) (int, error) {
	aop.mu.Lock()
	defer aop.mu.Unlock()

	filepath := aop.FilePath()
	// Truncate the file to remove any corrupted data that may have been added.
	err := os.Truncate(filepath, int64(aop.metadata.Length))
	if err != nil {
		return 0, errors.AddContext(err, "could not truncate file before write")
	}

	// Append data and sync
	numBytes, err := aop.staticF.WriteAt(b, int64(aop.metadata.Length))
	if err != nil {
		return 0, errors.AddContext(err, "unable to append new data to blacklist persist file")
	}
	err = aop.staticF.Sync()
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	aop.metadata.Length += uint64(numBytes)
	lengthBytes := encoding.Marshal(aop.metadata.Length)

	// Write to file
	lengthOffset := int64(2 * types.SpecifierLen)
	_, err = aop.staticF.WriteAt(lengthBytes, lengthOffset)
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to write length")
	}
	err = aop.staticF.Sync()
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to fsync file")
	}
	return numBytes, nil
}

// initOrLoadPersist initializes the persistence file if it doesn't exist or
// loads it from disk if it does and then returns the non-metadata bytes.
func (aop *AppendOnlyPersist) initOrLoadPersist(dir string) (io.Reader, error) {
	// Initialize the persistence directory
	err := os.MkdirAll(dir, defaultDirPermissions)
	if err != nil {
		return nil, errors.AddContext(err, "unable to make persistence directory")
	}

	// Try and load persistence.
	reader, err := aop.load()
	if err == nil {
		// Return the loaded persistence bytes.
		return reader, nil
	} else if !os.IsNotExist(err) {
		return nil, errors.AddContext(err, "unable to load persistence")
	}

	err = aop.init()
	return bytes.NewReader([]byte{}), err
}

// init initializes the persistence file.
func (aop *AppendOnlyPersist) init() (err error) {
	// Marshal the metadata.
	aop.metadata.Length = MetadataPageSize
	metadataBytes := encoding.Marshal(aop.metadata)

	// Sanity check that the metadataBytes are less than the MetadataPageSize
	if uint64(len(metadataBytes)) > MetadataPageSize {
		err := fmt.Errorf("metadataBytes too long, %v > %v", len(metadataBytes), MetadataPageSize)
		build.Critical(err)
		return err
	}

	// Create the persist file.
	f, err := os.OpenFile(aop.FilePath(), os.O_RDWR|os.O_CREATE, defaultFilePermissions)
	if err != nil {
		return errors.AddContext(err, "unable to open persistence file")
	}
	aop.staticF = f

	// Make sure to close the file if there is an error.
	defer func() {
		if err != nil {
			err = errors.Compose(err, f.Close())
		}
	}()

	// Write metadata to beginning of file. This is a small amount of data and
	// so operation is ACID as a single write and sync.
	_, err = aop.staticF.WriteAt(metadataBytes, 0)
	if err != nil {
		return errors.AddContext(err, "unable to write metadata to file on initialization")
	}
	err = aop.staticF.Sync()
	if err != nil {
		return errors.AddContext(err, "unable to fsync file")
	}

	return nil
}

// load loads the persist file from disk, returning the non-metadata bytes.
func (aop *AppendOnlyPersist) load() (_ io.Reader, err error) {
	// Open File
	filepath := aop.FilePath()
	f, err := os.OpenFile(filepath, os.O_RDWR, defaultFilePermissions)
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return nil, err
	}
	aop.staticF = f

	// Make sure to close the file if there is an error.
	defer func() {
		if err != nil {
			err = errors.Compose(err, f.Close())
		}
	}()

	// Check the Header and Version of the file
	metadataSize := uint64(2*types.SpecifierLen) + lengthSize
	metadataBytes := make([]byte, metadataSize)
	_, err = aop.staticF.ReadAt(metadataBytes, 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to read metadata bytes from file")
	}
	var metadata appendOnlyPersistMetadata
	err = encoding.Unmarshal(metadataBytes, &metadata)
	if err != nil {
		return nil, errors.AddContext(err, "unable to unmarshal metadata bytes")
	}
	err = aop.updateMetadata(metadata)
	if err != nil {
		return nil, errors.AddContext(err, "unable to update metadata")
	}

	// Check if there are persisted objects after the metadata.
	goodBytes := aop.metadata.Length - MetadataPageSize
	if goodBytes <= 0 {
		return bytes.NewReader([]byte{}), nil
	}

	// Truncate the file to remove any corrupted data that may have been added.
	err = os.Truncate(filepath, int64(aop.metadata.Length))
	if err != nil {
		return nil, err
	}
	// Seek to the start of the persist data section.
	_, err = aop.staticF.Seek(int64(MetadataPageSize), 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to seek to start of persist file")
	}

	return aop.staticF, nil
}

// updateMetadata updates the metadata, validating its correctness.
func (aop *AppendOnlyPersist) updateMetadata(metadata appendOnlyPersistMetadata) error {
	if metadata.Header != aop.metadata.Header {
		// Convert headers to strings and strip newlines for displaying.
		expected := string(bytes.Split(aop.metadata.Header[:], []byte{'\n'})[0])
		received := string(bytes.Split(metadata.Header[:], []byte{'\n'})[0])
		return errors.AddContext(ErrWrongHeader, fmt.Sprintf("expected %v, received %v", expected, received))
	}
	if metadata.Version != aop.metadata.Version {
		// Convert versions to strings and strip newlines for displaying.
		expected := string(bytes.Split(aop.metadata.Version[:], []byte{'\n'})[0])
		received := string(bytes.Split(metadata.Version[:], []byte{'\n'})[0])
		return errors.AddContext(ErrWrongVersion, fmt.Sprintf("expected %v, received %v", expected, received))
	}

	aop.metadata = metadata
	return nil
}

// UnmarshalSia implements the encoding.SiaUnmarshaler interface.
func (aopm *appendOnlyPersistMetadata) UnmarshalSia(r io.Reader) error {
	raw, err := ioutil.ReadAll(r)
	if err != nil {
		return errors.AddContext(err, "unable to read bytes from reader when marshalling")
	}

	// Define offsets for reading from provided byte slice.
	versionOffset := types.SpecifierLen
	lengthOffset := 2 * types.SpecifierLen

	// Unmarshal and check header and version for correctness.
	err = aopm.Header.UnmarshalText(raw[:versionOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal header")
	}
	err = aopm.Version.UnmarshalText(raw[versionOffset:lengthOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal version")
	}

	// Unmarshal the length
	err = encoding.Unmarshal(raw[lengthOffset:], &aopm.Length)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persist length")
	}

	return nil
}
