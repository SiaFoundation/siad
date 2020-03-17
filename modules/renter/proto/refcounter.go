package proto

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/writeaheadlog"

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

	// updateNameAppend is the name of a WAL update that appends a single
	// counter to the refcounter file
	updateNameAppend = "APPEND"

	// updateNameWriteAt is the name of a WAL update that deletes the file
	// from disk
	updateNameDelete = writeaheadlog.NameDeleteUpdate

	// updateNameWriteAt is the name of a WAL update that changes the data
	// starting at a specified index
	updateNameWriteAt = writeaheadlog.NameWriteAtUpdate

	// updateNameTruncate is the name of a WAL update that truncates the
	// file on disk from a specified size to a specified size
	updateNameTruncate = writeaheadlog.NameTruncateUpdate
)

const (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = 8
)

type (
	// RefCounter keeps track of how many references to each sector exist.
	//
	// Once the number of references drops to zero we consider the sector as
	// garbage. We move the sector to end of the data and set the
	// GarbageCollectionOffset to point to it. We can either reuse it to store
	// new data or drop it from the contract at the end of the current period
	// and before the contract renewal.
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

	// u16 is a utility type for ser/des of uint16 values
	u16 [2]byte
)

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string, wal *writeaheadlog.WAL) (RefCounter, error) {
	// Open the file and start loading the data.
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
		wal:              wal,
	}, nil
}

// NewRefCounter creates a new sector reference counter file to accompany
// a contract file
func NewRefCounter(path string, numSec uint64, wal *writeaheadlog.WAL) (RefCounter, error) {
	h := RefCounterHeader{
		Version: RefCounterVersion,
	}
	updateHeader := writeaheadlog.WriteAtUpdate(path, 0, serializeHeader(h))

	b := make([]byte, numSec*2)
	for i := uint64(0); i < numSec; i++ {
		binary.LittleEndian.PutUint16(b[i*2:i*2+2], 1)
	}
	updateCounters := writeaheadlog.WriteAtUpdate(path, RefCounterHeaderSize, b)

	err := wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, updateHeader, updateCounters)
	return RefCounter{
		RefCounterHeader: h,
		filepath:         path,
		numSectors:       numSec,
		wal:              wal,
	}, err
}

// Append appends one counter to the end of the refcounter file and
// initializes it with `1`
func (rc *RefCounter) Append() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	updateResize := writeaheadlog.TruncateUpdate(rc.filepath, RefCounterHeaderSize+int64(rc.numSectors+1)*2)
	var b u16
	binary.LittleEndian.PutUint16(b[:], 1)
	updateWriteValue := writeaheadlog.WriteAtUpdate(rc.filepath, int64(offset(rc.numSectors)), b[:])
	err := rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, updateResize, updateWriteValue)
	if err != nil {
		return err
	}

	// increment only after a successful append
	rc.numSectors++
	return nil
}

// Count returns the number of references to the given sector
func (rc *RefCounter) Count(secIdx uint64) (uint16, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	return rc.readCount(secIdx)
}

// Decrement decrements the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *RefCounter) Decrement(secIdx uint64) (uint16, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return 0, errors.AddContext(err, "failed to read count")
	}
	if count == 0 {
		return 0, errors.New("sector count underflow")
	}
	count--

	var b u16
	binary.LittleEndian.PutUint16(b[:], count)
	update := writeaheadlog.WriteAtUpdate(rc.filepath, int64(offset(secIdx)), b[:])
	err = rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, update)
	return count, err
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	u := writeaheadlog.DeleteUpdate(rc.filepath)
	return rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, u)
}

// DropSectors removes the last numSec sector counts from the refcounter file
func (rc *RefCounter) DropSectors(numSec uint64) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if numSec > rc.numSectors {
		return ErrInvalidSectorNumber
	}

	update := writeaheadlog.TruncateUpdate(rc.filepath, RefCounterHeaderSize+int64(rc.numSectors-numSec)*2)
	err := rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, update)
	if err != nil {
		return err
	}

	// decrement only after a successful truncate
	rc.numSectors -= numSec
	return nil
}

// Increment increments the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *RefCounter) Increment(secIdx uint64) (uint16, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if secIdx > rc.numSectors-1 {
		return 0, ErrInvalidSectorNumber
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return 0, errors.AddContext(err, "failed to read count")
	}
	if count == math.MaxUint16 {
		return 0, errors.New("sector count overflow")
	}
	count++

	var b u16
	binary.LittleEndian.PutUint16(b[:], count)
	update := writeaheadlog.WriteAtUpdate(rc.filepath, int64(offset(secIdx)), b[:])
	err = rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, update)
	return count, err
}

// Swap swaps the two sectors at the given indices
func (rc *RefCounter) Swap(firstSector, secondSector uint64) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if firstSector > rc.numSectors-1 || secondSector > rc.numSectors-1 {
		return ErrInvalidSectorNumber
	}

	// get the values to be swapped
	firstCount, err := rc.readCount(firstSector)
	if err != nil {
		return err
	}
	secondCount, err := rc.readCount(secondSector)
	if err != nil {
		return err
	}
	var firstValue u16
	var secondValue u16
	binary.LittleEndian.PutUint16(firstValue[:], firstCount)
	binary.LittleEndian.PutUint16(secondValue[:], secondCount)
	firstOffset := int64(offset(firstSector))
	secondOffset := int64(offset(secondSector))

	// swap the values on disk
	updateFirst := writeaheadlog.WriteAtUpdate(rc.filepath, firstOffset, secondValue[:])
	updateSecond := writeaheadlog.WriteAtUpdate(rc.filepath, secondOffset, firstValue[:])
	return rc.wal.CreateAndApplyTransaction(writeaheadlog.ApplyUpdates, updateFirst, updateSecond)
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

	var b u16
	if _, err = f.ReadAt(b[:], int64(offset(secIdx))); err != nil {
		return 0, errors.AddContext(err, "failed to read from the refcounter file")
	}
	return binary.LittleEndian.Uint16(b[:]), nil
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
