package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/errors"
)

var (
	// ErrInvalidHeaderData is returned when we try to deserialize the header from
	// a []byte with incorrect data
	ErrInvalidHeaderData = errors.New("invalid header data")

	// ErrInvalidSectorNumber is returned when the requested sector doesnt' exist
	ErrInvalidSectorNumber = errors.New("invalid sector given - it does not exist")

	// RefCounterVersion defines the latest version of the RefCounter
	RefCounterVersion = [8]byte{1}
)

const (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = 8

	// we initialise the counters with this relatively high value, so we don't run
	// the risk of the sectors being accidentally marked as garbage if someone
	// deletes a number of backups before we've been able to get the actual
	// reference count value
	initialCounterValue = 1024
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

		filepath     string   // where the refcounter is persisted on disk
		sectorCounts []uint16 // number of references per sector
		mu           sync.Mutex
	}

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		Version [8]byte
	}
)

// IncrementCount increments the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the updated number of references or an error.
func (rc *RefCounter) IncrementCount(secNum uint64) (uint16, error) {
	if secNum >= uint64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	if rc.sectorCounts[secNum] == math.MaxUint16 {
		return 0, errors.New("sector count overflow")
	}
	rc.sectorCounts[secNum]++
	return rc.sectorCounts[secNum], rc.writeCount(secNum, rc.sectorCounts[secNum])
}

// DecrementCount decrements the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the updated number of references or an error.
func (rc *RefCounter) DecrementCount(secNum uint64) (uint16, error) {
	if secNum >= uint64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	if rc.sectorCounts[secNum] == 0 {
		return 0, errors.New("sector count underflow")
	}
	rc.sectorCounts[secNum]--
	if rc.sectorCounts[secNum] == 0 {
		if err := rc.managedSwap(secNum, uint64(len(rc.sectorCounts)-1)); err != nil {
			return 0, errors.AddContext(err, "failed to swap sectors")
		}
		if err := rc.managedTruncate(1); err != nil {
			return 0, errors.AddContext(err, "failed to truncate")
		}
		return 0, nil
	}
	return rc.sectorCounts[secNum], rc.writeCount(secNum, rc.sectorCounts[secNum])
}

// Count returns the number of references to the given sector
// Note: Use a pointer because a gigabyte file has >512B RefCounter object.
func (rc *RefCounter) Count(secNum uint64) (uint16, error) {
	if secNum >= uint64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	return rc.sectorCounts[secNum], nil
}

// DeleteRefCounter deletes the counter's file from disk
func (rc *RefCounter) DeleteRefCounter() (err error) {
	return os.Remove(rc.filepath)
}

// callSwap swaps the two sectors at the given indices. This affects both the contract file and reference counters
// in memory and on disk.
func callSwap(rc *RefCounter, i, j uint64) error {
	return rc.managedSwap(i, j)
}

// managedSwap swaps two sectors. This affects both the contract file and reference counters in memory and on disk.
func (rc *RefCounter) managedSwap(first, second uint64) error {
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	rc.mu.Lock()
	defer rc.mu.Unlock()
	// swap the values on disk
	// do this first in order to avoid de-sync between disk and memory in case of an error
	buf := make([]byte, 2)
	binary.LittleEndian.PutUint16(buf, rc.sectorCounts[first])
	if _, err = f.WriteAt(buf, int64(offset(second))); err != nil {
		return err
	}
	binary.LittleEndian.PutUint16(buf, rc.sectorCounts[second])
	if _, err = f.WriteAt(buf, int64(offset(first))); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}

	// swap the values in memory
	rc.sectorCounts[first], rc.sectorCounts[second] = rc.sectorCounts[second], rc.sectorCounts[first]
	return nil
}

// callTruncate removes the last `n` sector counts from the refcounter file
// both in memory and on disk
func callTruncate(rc *RefCounter, n uint64) error {
	return rc.managedTruncate(n)
}

// managedTruncate removes the last `n` sector counts from the refcounter file
// both in memory and on disk
func (rc *RefCounter) managedTruncate(n uint64) error {
	if n > uint64(len(rc.sectorCounts)) {
		return fmt.Errorf("cannot truncate more than the total number of counts. number of counts: %d, counts to truncate: %d", len(rc.sectorCounts), n)
	}

	rc.mu.Lock()
	defer rc.mu.Unlock()
	// truncate the file on disk
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := os.Stat(rc.filepath)
	if err != nil {
		return err
	}
	if err = f.Truncate(info.Size() - int64(n*2)); err != nil {
		return err
	}

	// truncate the data in memory
	rc.sectorCounts = rc.sectorCounts[:uint64(len(rc.sectorCounts))-n]
	return nil
}

// writeCount stores the given sector count on disk
func (rc *RefCounter) writeCount(secNum uint64, c uint16) error {
	f, err := os.OpenFile(rc.filepath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	bytes := make([]byte, 2)
	binary.LittleEndian.PutUint16(bytes, c)
	if _, err = f.WriteAt(bytes, int64(offset(secNum))); err != nil {
		return err
	}
	return f.Sync()
}

// NewRefCounter creates a new sector reference counter file to accompany a contract file
func NewRefCounter(path string, numSectors uint64) (RefCounter, error) {
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
	zeroCounts := make([]uint16, numSectors)
	for i := uint64(0); i < numSectors; i++ {
		zeroCounts[i] = initialCounterValue
		if err = binary.Write(f, binary.LittleEndian, uint16(initialCounterValue)); err != nil {
			return RefCounter{}, errors.AddContext(err, "failed to initialize file on disk")
		}
	}
	if err := f.Sync(); err != nil {
		return RefCounter{}, err
	}
	return RefCounter{
		RefCounterHeader: h,
		sectorCounts:     zeroCounts,
		filepath:         path,
	}, nil
}

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string) (RefCounter, error) {
	f, err := os.Open(path)
	if err != nil {
		return RefCounter{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
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

	numSectors := (stat.Size() - RefCounterHeaderSize) / 2
	sectorBytes := make([]byte, numSectors*2)
	if _, err = f.ReadAt(sectorBytes, RefCounterHeaderSize); err != nil {
		return RefCounter{}, err
	}
	sectorCounts := make([]uint16, numSectors)
	for i := int64(0); i < numSectors; i++ {
		sectorCounts[i] = binary.LittleEndian.Uint16(sectorBytes[i*2 : i*2+2])
	}
	return RefCounter{
		RefCounterHeader: header,
		filepath:         path,
		sectorCounts:     sectorCounts,
	}, nil
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
func offset(secNum uint64) uint64 {
	return RefCounterHeaderSize + secNum*2
}

// serializeHeader serializes a header to []byte
func serializeHeader(h RefCounterHeader) []byte {
	b := make([]byte, RefCounterHeaderSize)
	copy(b[:8], h.Version[:])
	return b
}
