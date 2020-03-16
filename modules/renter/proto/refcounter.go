package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

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

	// walFileExtension is the extension of the file which holds the WAL data on
	// disk
	walFileExtension = ".wal"
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

	// u16 is a utility type for ser/des of uint16 values
	u16 [2]byte
)

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string) (RefCounter, error) {
	// Open the file and start loading the data.
	f, err := os.Open(path)
	if err != nil {
		return RefCounter{}, err
	}
	defer f.Close()

	// Load the WAL and execute all outstanding transactions before doing
	// anything else. We need this to happen before reading the file because it
	// might affect the number of sectors. We also do it after we've checked
	// that the refcounter file exists, so we don't need to specifically clean
	// up the created .wal file.
	wal, err := openWAL(path + walFileExtension)
	if err != nil {
		return RefCounter{}, err
	}

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
func NewRefCounter(path string, numSec uint64) (RefCounter, error) {
	f, err := os.Create(path)
	if err != nil {
		return RefCounter{}, errors.AddContext(err, "Failed to create a file on disk")
	}
	defer f.Close()
	h := RefCounterHeader{
		Version: RefCounterVersion,
	}

	// Open the WAL.
	recoveredTxns, wal, err := writeaheadlog.New(path + walFileExtension)
	if err != nil {
		return RefCounter{}, err
	}
	// Ignore the recovered transactions - they are leftovers from a previous
	// file with the same name (unlikely as it is).
	for _, txn := range recoveredTxns {
		_ = txn.SignalUpdatesApplied
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
		wal:              wal,
	}, nil
}

// Append appends one counter to the end of the refcounter file and
// initializes it with `1`
func (rc *RefCounter) Append() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	update := makeAppendUpdate(rc.filepath, rc.numSectors)
	err := rc.wal.CreateAndApplyTransaction(applyUpdates, update)
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
	err = rc.wal.CreateAndApplyTransaction(applyUpdates, update)
	return count, err
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	u := writeaheadlog.DeleteUpdate(rc.filepath)
	return rc.wal.CreateAndApplyTransaction(applyUpdates, u)
}

// DropSectors removes the last numSec sector counts from the refcounter file
func (rc *RefCounter) DropSectors(numSec uint64) error {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if numSec > rc.numSectors {
		return ErrInvalidSectorNumber
	}

	update := writeaheadlog.TruncateUpdate(rc.filepath, RefCounterHeaderSize+int64(rc.numSectors-numSec)*2)
	err := rc.wal.CreateAndApplyTransaction(applyUpdates, update)
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
	err = rc.wal.CreateAndApplyTransaction(applyUpdates, update)
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
	return rc.wal.CreateAndApplyTransaction(applyUpdates, updateFirst, updateSecond)
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

	var b u16
	binary.LittleEndian.PutUint16(b[:], c)
	if _, err = f.WriteAt(b[:], int64(offset(secIdx))); err != nil {
		return err
	}
	return f.Sync()
}

// applyAppendUpdate executes an `updateNameAppend` WAL update
func applyAppendUpdate(u writeaheadlog.Update) error {
	if u.Name != updateNameAppend {
		return fmt.Errorf("applyAppendUpdate called on update of type %v", u.Name)
	}

	// Decode update.
	if len(u.Instructions) < 8 {
		return errors.New("instructions slice of update is too short to contain the numSectors and path")
	}
	numSectors := binary.LittleEndian.Uint64(u.Instructions[:8])
	path := string(u.Instructions[8:])

	// Resize the file on disk.
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	var b u16
	binary.LittleEndian.PutUint16(b[:], 1)
	offset := int64(offset(numSectors))
	if _, err = f.WriteAt(b[:], offset); err != nil {
		return errors.AddContext(err, "failed to write new counter to disk")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	return nil
}

// applyUpdates executes a list of WAL updates
func applyUpdates(updates ...writeaheadlog.Update) error {
	for _, update := range updates {
		var err error
		switch update.Name {
		case updateNameAppend:
			err = applyAppendUpdate(update)
		case updateNameDelete:
			err = writeaheadlog.ApplyDeleteUpdate(update)
		case updateNameTruncate:
			err = writeaheadlog.ApplyTruncateUpdate(update)
		case updateNameWriteAt:
			err = writeaheadlog.ApplyWriteAtUpdate(update)
		default:
			err = fmt.Errorf("unknown update type: %v", update.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// deserializeHeader deserializes a header from []byte
func deserializeHeader(b []byte, h *RefCounterHeader) error {
	if uint64(len(b)) < RefCounterHeaderSize {
		return ErrInvalidHeaderData
	}
	copy(h.Version[:], b[:8])
	return nil
}

// makeAppendUpdate creates a writeaheadlog update for appending the specified
// file. The `oldNumSectors` value is the value before the append operation.
func makeAppendUpdate(path string, oldNumSectors uint64) writeaheadlog.Update {
	// Create update
	i := make([]byte, 8+len(path))
	binary.LittleEndian.PutUint64(i[:8], oldNumSectors)
	copy(i[8:], path)
	return writeaheadlog.Update{
		Name:         updateNameAppend,
		Instructions: i,
	}
}

// offset calculates the byte offset of the sector counter in the file on disk
func offset(secIdx uint64) uint64 {
	return RefCounterHeaderSize + secIdx*2
}

// openWAL loads a WAL from a file on disk
func openWAL(walPath string) (*writeaheadlog.WAL, error) {
	// Open the WAL.
	recoveredTxns, wal, err := writeaheadlog.New(walPath)
	if err != nil {
		return &writeaheadlog.WAL{}, err
	}

	if len(recoveredTxns) != 0 {
		// Apparently the system crashed. Handle the unfinished updates
		// accordingly.
		//
		// After the recovery is complete we can signal the WAL that we are
		// done and that the updates were applied.
		// NOTE: This is optional. If for some reason an update cannot be
		// applied right away it may be skipped and applied later
		for _, txn := range recoveredTxns {
			if err := applyUpdates(txn.Updates...); err != nil {
				return wal, err
			}
			// this is optional, so we can ignore the error
			_ = txn.SignalUpdatesApplied()
		}
	}
	return wal, nil
}

// serializeHeader serializes a header to []byte
func serializeHeader(h RefCounterHeader) []byte {
	b := make([]byte, RefCounterHeaderSize)
	copy(b[:8], h.Version[:])
	return b
}
