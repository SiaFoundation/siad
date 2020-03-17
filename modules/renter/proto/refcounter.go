package proto

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"

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

	// UpdateNameAppend is the name of an idempotent update that appends a
	// single counter to the file and initialises it with 1.
	UpdateNameAppend = "RC_APPEND"

	// UpdateNameDelete is the name of an idempotent update that deletes a file
	// from the disk.
	UpdateNameDelete = "RC_DELETE"

	// UpdateNameTruncate is the name of an idempotent update that truncates a
	// refcounter file by a number of sectors.
	UpdateNameTruncate = "RC_TRUNCATE"

	// UpdateNameWriteAt is the name of an idempotent update that writes a
	// given value at a given position in a refcounter file.
	UpdateNameWriteAt = "RC_WRITE_AT"
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

// CreateAppendUpdate is a helper function which creates a writeaheadlog update
// for appending a single counter with value 1 to the end of the file.
func (rc *RefCounter) CreateAppendUpdate() writeaheadlog.Update {
	b := make([]byte, 8+4+len(rc.filepath))
	binary.LittleEndian.PutUint64(b[:8], rc.numSectors)
	binary.LittleEndian.PutUint32(b[8:12], uint32(len(rc.filepath)))
	copy(b[12:12+len(rc.filepath)], rc.filepath)
	return writeaheadlog.Update{
		Name:         UpdateNameAppend,
		Instructions: b,
	}
}

// ApplyAppendUpdate parses and applies an Append update.
func (rc *RefCounter) ApplyAppendUpdate(u writeaheadlog.Update) error {
	if u.Name != UpdateNameAppend {
		return fmt.Errorf("applyAppendUpdate called on update of type %v", u.Name)
	}
	// Decode update.
	if len(u.Instructions) < 12 {
		return errors.New("instructions slice of update is too short to contain the size and path")
	}
	oldSecNum := binary.LittleEndian.Uint64(u.Instructions[:8])
	pathLen := int64(binary.LittleEndian.Uint64(u.Instructions[8:12]))
	path := string(u.Instructions[12 : 12+pathLen])

	// Verify that we have the correct starting number of sectors.
	if oldSecNum != rc.numSectors {
		return fmt.Errorf("current number of sector expected to be %d but it is %d", oldSecNum, rc.numSectors)
	}
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	// Append a single counter to the end of the file.
	var b u16
	binary.LittleEndian.PutUint16(b[:], 1)
	if _, err = f.WriteAt(b[:], int64(offset(oldSecNum))); err != nil {
		return err
	}
	// After the successful Append we need to adjust the in-memory number of sectors
	rc.numSectors++
	return f.Sync()
}

func (rc *RefCounter) CreateDeleteUpdate() writeaheadlog.Update {
	return writeaheadlog.DeleteUpdate(rc.filepath)
}

func (rc *RefCounter) ApplyDeleteUpdate(update writeaheadlog.Update) error {
	return writeaheadlog.ApplyDeleteUpdate(update)
}

// CreateTruncateUpdate is a helper function which creates a writeaheadlog
// update for truncating a number of sectors from the end of the file.
func (rc *RefCounter) CreateTruncateUpdate(numSecsToDrop uint64) writeaheadlog.Update {
	b := make([]byte, 8+4+len(rc.filepath))
	binary.LittleEndian.PutUint64(b[:8], numSecsToDrop)
	binary.LittleEndian.PutUint64(b[8:16], rc.numSectors)
	binary.LittleEndian.PutUint32(b[16:20], uint32(len(rc.filepath)))
	copy(b[20:20+len(rc.filepath)], rc.filepath)
	return writeaheadlog.Update{
		Name:         UpdateNameTruncate,
		Instructions: b,
	}
}

// ApplyTruncateUpdate parses and applies a Truncate update.
func (rc *RefCounter) ApplyTruncateUpdate(u writeaheadlog.Update) error {
	if u.Name != UpdateNameTruncate {
		return fmt.Errorf("applyAppendTruncate called on update of type %v", u.Name)
	}
	// Decode update.
	if len(u.Instructions) < 20 {
		return errors.New("instructions slice of update is too short to contain the size and path")
	}
	numSecsToDrop := binary.LittleEndian.Uint64(u.Instructions[:8])
	oldSecNum := binary.LittleEndian.Uint64(u.Instructions[8:16])
	pathLen := int64(binary.LittleEndian.Uint64(u.Instructions[16:20]))
	path := string(u.Instructions[20 : 20+pathLen])

	// Verify that we have the correct starting number of sectors.
	if oldSecNum != rc.numSectors {
		return fmt.Errorf("current number of sector expected to be %d but it is %d", oldSecNum, rc.numSectors)
	}
	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	// Truncate the file to the needed size.
	if err = f.Truncate(int64(RefCounterHeaderSize + (oldSecNum-numSecsToDrop)*2)); err != nil {
		return err
	}
	// After the successful Truncate we need to adjust the in-memory number of sectors
	rc.numSectors -= numSecsToDrop
	return f.Sync()
}

// CreateWriteAtUpdate is a helper function which creates a writeaheadlog
// update for writing some data at a given position in the file.
func (rc *RefCounter) CreateWriteAtUpdate(secIdx uint64, value uint16) writeaheadlog.Update {
	b := make([]byte, 8+2+4+len(rc.filepath))
	binary.LittleEndian.PutUint64(b[:8], secIdx)
	binary.LittleEndian.PutUint16(b[8:10], value)
	binary.LittleEndian.PutUint32(b[10:14], uint32(len(rc.filepath)))
	copy(b[14:14+len(rc.filepath)], rc.filepath)
	return writeaheadlog.Update{
		Name:         UpdateNameWriteAt,
		Instructions: b,
	}
}

// ApplyWriteAtUpdate parses and applies a WriteAt update.
func (rc *RefCounter) ApplyWriteAtUpdate(u writeaheadlog.Update) error {
	if u.Name != UpdateNameWriteAt {
		return fmt.Errorf("applyAppendWriteAt called on update of type %v", u.Name)
	}
	// Decode update.
	if len(u.Instructions) < 14 {
		return errors.New("instructions slice of update is too short to contain the size and path")
	}
	secIdx := binary.LittleEndian.Uint64(u.Instructions[:8])
	// We don't need to decode the value because we need it as a []byte anyway.
	value := u.Instructions[8:10]
	pathLen := int64(binary.LittleEndian.Uint64(u.Instructions[10:14]))
	path := string(u.Instructions[14 : 14+pathLen])

	f, err := os.OpenFile(path, os.O_RDWR, modules.DefaultFilePerm)
	if err != nil {
		return err
	}
	defer f.Close()

	// Write the data to the  file.
	if _, err = f.WriteAt(value, int64(offset(secIdx))); err != nil {
		return err
	}
	return f.Sync()
}

func (rc *RefCounter) ApplyUpdates(updates ...writeaheadlog.Update) error {
	// TODO: This is the next stage. We just need to also filter the Deletes
	// 	because we can't process them while holding an open file handler.
	//
	//f, err := os.OpenFile(rc.filepath, os.O_RDWR, modules.DefaultFilePerm)
	//if err != nil {
	//	return err
	//}
	//defer func() {
	//	f.Sync()
	//	f.Close()
	//}()
	for _, update := range updates {
		var err error
		switch update.Name {
		case UpdateNameAppend:
			err = rc.ApplyAppendUpdate(update)
		case UpdateNameDelete:
			err = rc.ApplyDeleteUpdate(update)
		case UpdateNameTruncate:
			err = rc.ApplyTruncateUpdate(update)
		case UpdateNameWriteAt:
			err = rc.ApplyWriteAtUpdate(update)
		default:
			err = fmt.Errorf("unknown update type: %v", update.Name)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

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
func (rc *RefCounter) Append() writeaheadlog.Update {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.CreateAppendUpdate()
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
func (rc *RefCounter) Decrement(secIdx uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if secIdx > rc.numSectors-1 {
		return writeaheadlog.Update{}, ErrInvalidSectorNumber
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to read count")
	}
	if count == 0 {
		return writeaheadlog.Update{}, errors.New("sector count underflow")
	}
	count--
	return rc.CreateWriteAtUpdate(secIdx, count), nil
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() writeaheadlog.Update {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return rc.CreateDeleteUpdate()
}

// DropSectors removes the last numSec sector counts from the refcounter file
func (rc *RefCounter) DropSectors(numSec uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if numSec > rc.numSectors {
		return writeaheadlog.Update{}, ErrInvalidSectorNumber
	}
	return rc.CreateTruncateUpdate(numSec), nil
}

// Increment increments the reference counter of a given sector. The sector
// is specified by its sequential number (secIdx).
// Returns the updated number of references or an error.
func (rc *RefCounter) Increment(secIdx uint64) (writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if secIdx > rc.numSectors-1 {
		return writeaheadlog.Update{}, ErrInvalidSectorNumber
	}
	count, err := rc.readCount(secIdx)
	if err != nil {
		return writeaheadlog.Update{}, errors.AddContext(err, "failed to read count")
	}
	if count == math.MaxUint16 {
		return writeaheadlog.Update{}, errors.New("sector count overflow")
	}
	count++
	return rc.CreateWriteAtUpdate(secIdx, count), nil
}

// Swap swaps the two sectors at the given indices
func (rc *RefCounter) Swap(firstSector, secondSector uint64) ([]writeaheadlog.Update, error) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if firstSector > rc.numSectors-1 || secondSector > rc.numSectors-1 {
		return []writeaheadlog.Update{}, ErrInvalidSectorNumber
	}

	// get the values to be swapped
	firstCount, err := rc.readCount(firstSector)
	if err != nil {
		return []writeaheadlog.Update{}, err
	}
	secondCount, err := rc.readCount(secondSector)
	if err != nil {
		return []writeaheadlog.Update{}, err
	}

	// swap the values on disk
	updateFirst := rc.CreateWriteAtUpdate(firstSector, secondCount)
	updateSecond := rc.CreateWriteAtUpdate(secondSector, firstCount)
	return []writeaheadlog.Update{updateFirst, updateSecond}, nil
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
