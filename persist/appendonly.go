package persist

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
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
		staticPath       string
		staticObjectSize uint64

		metadata appendOnlyPersistMetadata

		mu sync.Mutex
	}

	appendOnlyPersistMetadata struct {
		staticHeader  types.Specifier
		staticVersion types.Specifier

		length uint64
	}
)

// NewAppendOnlyPersist creates a new AppendOnlyPersist object and initializes
// the persistence file.
func NewAppendOnlyPersist(dir, file string, size uint64, metadataHeader, metadataVersion types.Specifier) (*AppendOnlyPersist, []byte, error) {
	aop := &AppendOnlyPersist{
		staticPath:       filepath.Join(dir, file),
		staticObjectSize: size,
		metadata: appendOnlyPersistMetadata{
			staticHeader:  metadataHeader,
			staticVersion: metadataVersion,
		},
	}
	bytes, err := aop.initOrLoadPersist(dir)
	return aop, bytes, err
}

// FilePath returns the filepath of the persist file.
func (aop *AppendOnlyPersist) FilePath() string {
	return aop.staticPath
}

// PersistLength returns the length of the persist data.
func (aop *AppendOnlyPersist) PersistLength() uint64 {
	aop.mu.Lock()
	defer aop.mu.Unlock()

	return aop.metadata.length
}

// Write implements the io.Writer interface. It updates the persist file,
// appending the changes to the persist file on disk.
func (aop *AppendOnlyPersist) Write(b []byte) (int, error) {
	aop.mu.Lock()
	defer aop.mu.Unlock()

	filepath := aop.FilePath()
	// Truncate the file to remove any corrupted data that may have been added.
	err := os.Truncate(filepath, int64(aop.metadata.length))
	if err != nil {
		return 0, err
	}
	// Open file
	f, err := os.OpenFile(filepath, os.O_RDWR, defaultFilePermissions)
	if err != nil {
		return 0, errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Append data and sync
	numBytes, err := f.WriteAt(b, int64(aop.metadata.length))
	if err != nil {
		return 0, errors.AddContext(err, "unable to append new data to blacklist persist file")
	}
	err = f.Sync()
	if err != nil {
		return numBytes, errors.AddContext(err, "unable to fsync file")
	}

	// Update length and sync
	aop.metadata.length += uint64(numBytes)
	lengthBytes := encoding.Marshal(aop.metadata.length)

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

// initOrLoadPersist initializes the persistence file if it doesn't exist or
// loads it from disk if it does and then returns the non-metadata bytes.
func (aop *AppendOnlyPersist) initOrLoadPersist(dir string) ([]byte, error) {
	// Initialize the persistence directory
	err := os.MkdirAll(dir, defaultDirPermissions)
	if err != nil {
		return nil, errors.AddContext(err, "unable to make persistence directory")
	}

	// Try and load persistence.
	bytes, err := aop.load()
	if err == nil {
		// Return the loaded persistence bytes.
		return bytes, nil
	} else if !os.IsNotExist(err) {
		return nil, errors.AddContext(err, "unable to load persistence")
	}

	// Persist file doesn't exist, create it.
	f, err := os.OpenFile(aop.FilePath(), os.O_RDWR|os.O_CREATE, defaultFilePermissions)
	if err != nil {
		return nil, errors.AddContext(err, "unable to open persistence file")
	}
	defer f.Close()

	// Marshal the metadata.
	aop.metadata.length = MetadataPageSize
	metadataBytes := encoding.Marshal(aop.metadata)

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

	// Return empty bytes since there were no persistence bytes.
	return []byte{}, nil
}

// load loads the persist file from disk, returning the non-metadata bytes.
func (aop *AppendOnlyPersist) load() ([]byte, error) {
	// Open File
	filepath := aop.FilePath()
	f, err := os.Open(filepath)
	if err != nil {
		// Intentionally don't add context to allow for IsNotExist error check
		return nil, err
	}
	defer f.Close()

	// Check the Header and Version of the file
	metadataSize := uint64(2*types.SpecifierLen) + lengthSize
	metadataBytes := make([]byte, metadataSize)
	_, err = f.ReadAt(metadataBytes, 0)
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
	goodBytes := aop.metadata.length - MetadataPageSize
	if goodBytes <= 0 {
		return []byte{}, nil
	}

	// Truncate the file to remove any corrupted data that may have been added.
	err = os.Truncate(filepath, int64(aop.metadata.length))
	if err != nil {
		return nil, err
	}
	// Seek to the start of the persist file.
	_, err = f.Seek(int64(MetadataPageSize), 0)
	if err != nil {
		return nil, errors.AddContext(err, "unable to seek to start of persist file")
	}

	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	return bytes, nil
}

// updateMetadata updates the metadata, validating its correctness.
func (aop *AppendOnlyPersist) updateMetadata(metadata appendOnlyPersistMetadata) error {
	if metadata.staticHeader != aop.metadata.staticHeader {
		// Convert headers to strings and strip newlines for displaying.
		expected := string(bytes.Split(aop.metadata.staticHeader[:], []byte{'\n'})[0])
		received := string(bytes.Split(metadata.staticHeader[:], []byte{'\n'})[0])
		return errors.AddContext(ErrWrongHeader, fmt.Sprintf("expected %v, received %v", expected, received))
	}
	if metadata.staticVersion != aop.metadata.staticVersion {
		// Convert versions to strings and strip newlines for displaying.
		expected := string(bytes.Split(aop.metadata.staticVersion[:], []byte{'\n'})[0])
		received := string(bytes.Split(metadata.staticVersion[:], []byte{'\n'})[0])
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
	err = aopm.staticHeader.UnmarshalText(raw[:versionOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal header")
	}
	err = aopm.staticVersion.UnmarshalText(raw[versionOffset:lengthOffset])
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal version")
	}

	// Unmarshal the length
	err = encoding.Unmarshal(raw[lengthOffset:], &aopm.length)
	if err != nil {
		return errors.AddContext(err, "unable to unmarshal persist length")
	}

	return nil
}
