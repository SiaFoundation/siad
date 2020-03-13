package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/build"

	"gitlab.com/NebulousLabs/Sia/encoding"

	"gitlab.com/NebulousLabs/writeaheadlog"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInvalidHeaderData is returned when we try to deserialize the header from
	// a []byte with incorrect data
	ErrInvalidHeaderData = errors.New("invalid header data")

	// ErrInvalidSectorNumber is returned when the requested sector doesnt' exist
	ErrInvalidSectorNumber = errors.New("invalid sector given - it does not exist")

	// ErrInvalidVersion is returned when the version of the file we are trying to
	// read does not match the current RefCounterHeaderSize
	ErrInvalidVersion = errors.New("invalid file version")

	// RefCounterVersion defines the latest version of the RefCounter
	RefCounterVersion = [8]byte{1}

	// errUnknownRefCounterUpdate is returned when applyUpdates finds an update
	// that is unknown
	errUnknownRefCounterUpdate = errors.New("unknown refcounter update")
)

const (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = 8

	// updateNameSetValue is the name of a WAL update that deletes the file from disk
	updateNameDelete = "WALDelete"

	// updateNameSetValue is the name of a WAL update that changes the data starting
	// at a specified index
	updateNameSetValue = "WALSetValue"

	// updateNameResize is the name of a WAL update that changes the size of the
	// file on disk from a specified size to a specified size
	updateNameResize = "WALResize"
)

type (
	// RefCounter keeps track of how many references to each sector exist.
	//
	// Once the number of references drops to zero we consider the sector as
	// garbage. We move the sector to end of the data and set the
	// GarbageCollectionOffset to point to it. We can either reuse it to store new
	// data or drop it from the contract at the end of the current period and
	// before the contract renewal.
	RefCounter struct {
		RefCounterHeader

		filepath   string // where the refcounter is persisted on disk
		numSectors uint64 // used for sanity checks before we attempt mutation operations
		wal        *writeaheadlog.WAL
		mu         sync.Mutex
	}

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		Version [8]byte
	}

	// updateDelete represents a WAL update for deleting the refcounter file
	updateDelete struct {
		filepath string
	}
	// updateResize represents a WAL update for resizing the refcounter file
	// from an old number of sectors to a new one. This update can be used to
	// both shrink and grow the file
	updateResize struct {
		filepath  string
		oldSecNum uint64
		newSecNum uint64
	}
	// updateSetValue represents a WAL update for setting a given value to the
	// given sector
	updateSetValue struct {
		filepath string
		secNum   uint64
		value    uint16
	}
)

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string) (RefCounter, error) {
	f, err := os.Open(path)
	if err != nil {
		return RefCounter{}, err
	}
	defer f.Close()

	var header RefCounterHeader
	headerBytes := make([]byte, RefCounterHeaderSize)
	if _, err = f.ReadAt(headerBytes, 0); err != nil {
		return RefCounter{}, errors.AddContext(err, "unable to read from file")
	}
	if err = deserializeHeader(headerBytes, &header); err != nil {
		return RefCounter{}, errors.AddContext(err, "unable to load refcounter header")
	}
	if header.Version != RefCounterVersion {
		return RefCounter{}, errors.AddContext(ErrInvalidVersion, fmt.Sprintf("expected version %d, got version %d", RefCounterVersion, header.Version))
	}
	fi, err := os.Stat(path)
	if err != nil {
		return RefCounter{}, errors.AddContext(err, "failed to read file stats")
	}
	numSectors := uint64((fi.Size() - RefCounterHeaderSize) / 2)
	return RefCounter{
		RefCounterHeader: header,
		filepath:         path,
		numSectors:       numSectors,
	}, nil
}

// NewRefCounter creates a new sector reference counter file to accompany a contract file
func NewRefCounter(path string, numSec uint64) (RefCounter, error) {
	f, err := os.Create(path)
	if err != nil {
		return RefCounter{}, errors.AddContext(err, "Failed to create a file on disk")
	}
	defer f.Close()
	h := RefCounterHeader{
		Version: RefCounterVersion,
	}

	if _, err := f.WriteAt(serializeHeader(h), 0); err != nil {
		return RefCounter{}, err
	}

	if _, err = f.Seek(RefCounterHeaderSize, io.SeekStart); err != nil {
		return RefCounter{}, err
	}
	for i := uint64(0); i < numSec; i++ {
		if err = binary.Write(f, binary.LittleEndian, uint16(1)); err != nil {
			return RefCounter{}, errors.AddContext(err, "failed to initialize file on disk")
		}
	}
	if err := f.Sync(); err != nil {
		return RefCounter{}, err
	}
	return RefCounter{
		RefCounterHeader: h,
		filepath:         path,
		numSectors:       numSec,
	}, nil
}

// Count returns the number of references to the given sector
func (rc *RefCounter) Count(secIdx uint64) (uint16, error) {
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.readCount(secIdx)
}

// Decrement decrements the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *RefCounter) Decrement(secIdx uint64) (uint16, error) {
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	count, err := rc.readCount(secIdx)
	if err != nil {
		return 0, errors.AddContext(err, "failed to read count")
	}
	if count == 0 {
		return 0, errors.New("sector count underflow")
	}
	count--
	return count, rc.writeCount(secIdx, count)
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() (err error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return os.Remove(rc.filepath)
}

// Increment increments the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *RefCounter) Increment(secIdx uint64) (uint16, error) {
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	count, err := rc.readCount(secIdx)
	if err != nil {
		return 0, errors.AddContext(err, "failed to read count")
	}
	if count == math.MaxUint16 {
		return 0, errors.New("sector count overflow")
	}
	count++
	return count, rc.writeCount(secIdx, count)
}

// callAppend appends one counter to the end of the refcounter file and
// initializes it with `1`
func (rc *RefCounter) callAppend() error {
	return rc.managedAppend()
}

// callDropSectors removes the last numSec sector counts from the refcounter file
func (rc *RefCounter) callDropSectors(numSec uint64) error {
	return rc.managedDropSectors(numSec)
}

// callSwap swaps the two sectors at the given indices
func (rc *RefCounter) callSwap(i, j uint64) error {
	return rc.managedSwap(i, j)
}

// makeUpdateSetValue creates a WAL update for setting a given value to the
// given sector
func (rc *RefCounter) makeUpdateSetValue(secNum uint64, value uint16) writeaheadlog.Update {
	if secNum < 0 {
		secNum = 0
		value = 0
		build.Critical("secNum passed to makeUpdateSetValue should never be negative")
	}
	return writeaheadlog.Update{
		Name: updateNameSetValue,
		Instructions: encoding.MarshalAll(updateSetValue{
			filepath: rc.filepath,
			secNum:   secNum,
			value:    value,
		}),
	}
}

// makeUpdateResize creates a WAL update for resizing the refcounter file from
// an old number of sectors to a new one. This update can be used to both shrink
// and grow the file
func (rc *RefCounter) makeUpdateResize(oldSecNum, newSecNum uint64) writeaheadlog.Update {
	if oldSecNum < 0 || newSecNum < 0 {
		oldSecNum, newSecNum = 0, 0
		build.Critical("size passed to createResizeUpdate should never be negative")
	}
	return writeaheadlog.Update{
		Name: updateNameResize,
		Instructions: encoding.MarshalAll(updateResize{
			filepath:  rc.filepath,
			oldSecNum: oldSecNum,
			newSecNum: newSecNum,
		}),
	}
}

// makeUpdateDelete creates a WAL update for deleting the refcounter file
func (rc *RefCounter) makeUpdateDelete() writeaheadlog.Update {
	return writeaheadlog.Update{
		Name:         updateNameDelete,
		Instructions: encoding.Marshal(updateDelete{filepath: rc.filepath}),
	}
}

// managedAppend appends one counter to the end of the refcounter file and
// initializes it with `1``
func (rc *RefCounter) managedAppend() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	// resize the file on disk
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	b := make([]byte, 2)
	binary.LittleEndian.PutUint16(b, 1)
	offset := int64(offset(rc.numSectors))
	if _, err = f.WriteAt(b, offset); err != nil {
		return errors.AddContext(err, "failed to write new counter to disk")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// increment only after a successful append
	rc.numSectors++
	return nil
}

// managedDropSectors removes the last numSec sector counts from the refcounter
// file
func (rc *RefCounter) managedDropSectors(numSec uint64) error {
	if numSec > rc.numSectors {
		return ErrInvalidSectorNumber
	}
	rc.mu.Lock()
	defer rc.mu.Unlock()
	// truncate the file on disk
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	err = f.Truncate(RefCounterHeaderSize + int64(rc.numSectors-numSec)*2)
	if err != nil {
		return err
	}
	// decrement only after a successful truncate
	rc.numSectors -= numSec
	return nil
}

// managedSwap swaps the counts of the two sectors
func (rc *RefCounter) managedSwap(firstSector, secondSector uint64) error {
	if firstSector > rc.numSectors-1 || secondSector > rc.numSectors-1 {
		return ErrInvalidSectorNumber
	}
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	rc.mu.Lock()
	defer rc.mu.Unlock()
	// swap the values on disk
	firstOffset := int64(offset(firstSector))
	secondOffset := int64(offset(secondSector))
	firstCount := make([]byte, 2)
	secondCount := make([]byte, 2)
	if _, err = f.ReadAt(firstCount, firstOffset); err != nil {
		return err
	}
	if _, err = f.ReadAt(secondCount, secondOffset); err != nil {
		return err
	}
	if _, err = f.WriteAt(firstCount, secondOffset); err != nil {
		return err
	}
	if _, err = f.WriteAt(secondCount, firstOffset); err != nil {
		return err
	}
	return f.Sync()
}

// readCount reads the given sector count from disk
func (rc *RefCounter) readCount(secIdx uint64) (uint16, error) {
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	f, err := os.Open(rc.filepath)
	if err != nil {
		return 0, errors.AddContext(err, "failed to open the refcounter file")
	}
	defer f.Close()

	b := make([]byte, 2)
	if _, err = f.ReadAt(b, int64(offset(secIdx))); err != nil {
		return 0, errors.AddContext(err, "failed to read from the refcounter file")
	}
	return binary.LittleEndian.Uint16(b), nil
}

// writeCount stores the given sector count on disk
func (rc *RefCounter) writeCount(secIdx uint64, c uint16) error {
	if secIdx > rc.numSectors-1 {
		return ErrInvalidSectorNumber
	}
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	bytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(bytes, c)
	if _, err = f.WriteAt(bytes, int64(offset(secIdx))); err != nil {
		return err
	}
	return f.Sync()
}

// deserializeHeader deserializes a header from []byte
func deserializeHeader(b []byte, h *RefCounterHeader) error {
	if uint64(len(b)) < RefCounterHeaderSize {
		return ErrInvalidHeaderData
	}
	copy(h.Version[:], b[:8])
	return nil
}

// offset calculates the byte offset of the sector counter in the file on disk
func offset(secIdx uint64) uint64 {
	return RefCounterHeaderSize + secIdx*2
}

// serializeHeader serializes a header to []byte
func serializeHeader(h RefCounterHeader) []byte {
	b := make([]byte, RefCounterHeaderSize)
	copy(b[:8], h.Version[:])
	return b
}
