package proto

import (
	"encoding/binary"
	"math"
	"os"
	"sync"

	"gitlab.com/NebulousLabs/Sia/encoding"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = int64(56)

	// RefCounterVersion defines the latest version of the RefCounter
	// TODO: How do we actually do versioning? Check other structs.
	RefCounterVersion = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}

	// ErrInvalidSectorNumber is thrown when the requested sector doesnt' exist
	ErrInvalidSectorNumber = errors.New("invalid sector given - it does not exist")
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

		// Holds the number of references to that sector. Uses 2 bytes per sector.
		sectorCounts []uint16

		headerFS *fileSection
		countsFS *fileSection
		mu       sync.Mutex
	}

	// TODO: Persist filename

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		version [8]byte
	}
)

// IncrementCount increments the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the updated number of references or an error.
func (rc *RefCounter) IncrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	if rc.sectorCounts[secNum] == math.MaxUint16 {
		// TODO: Handle the overflow!
	}
	rc.sectorCounts[secNum]++
	secBytes := make([]byte, 2, 2)
	binary.LittleEndian.PutUint16(secBytes, rc.sectorCounts[secNum])
	rc.countsFS.WriteAt(secBytes, secNum*2)
	return rc.sectorCounts[secNum], nil
}

// DecrementCount decrements the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the oupdated number of references or an error.
func (rc *RefCounter) DecrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	if rc.sectorCounts[secNum] == 0 { // handle the case where it might already be zero
		rc.managedMarkSectorAsGarbage(secNum)
		return 0, nil
	}
	rc.sectorCounts[secNum]--
	secBytes := make([]byte, 2, 2)
	binary.LittleEndian.PutUint16(secBytes, rc.sectorCounts[secNum])
	rc.countsFS.WriteAt(secBytes, secNum*2)
	return rc.sectorCounts[secNum], nil
}

// GetCount returns the number of references to the given sector
// Note: Use a pointer because a gigabyte file has >512B RefCounter object.
func (rc *RefCounter) GetCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= int64(len(rc.sectorCounts)) {
		return 0, ErrInvalidSectorNumber
	}
	return rc.sectorCounts[secNum], nil
}

func (rc *RefCounter) managedMarkSectorAsGarbage(secNum int64) {
	// this method is unexported and the secNum validation is already done.
	// TODO: get the contract, swap the sectors, recalculate the merkle root (?)
	// and then execute the code below
	rc.mu.Lock()
	rc.sectorCounts[secNum] = rc.sectorCounts[len(rc.sectorCounts)-1]
	rc.sectorCounts = rc.sectorCounts[:len(rc.sectorCounts)-1]
	rc.mu.Unlock()
}

// NewRefCounter creates a new sector reference counter file to accompany a contract file
func NewRefCounter(path string, numSectors int64) (RefCounter, error) {
	f, err := os.Create(path)
	if err != nil {
		return RefCounter{}, err
	}
	h := RefCounterHeader{
		version: RefCounterVersion,
	}
	// create fileSections
	headerSection := newFileSection(f, 0, RefCounterHeaderSize)
	countsSection := newFileSection(f, RefCounterHeaderSize, remainingFile)
	// write header
	if _, err := headerSection.WriteAt(encoding.Marshal(h), 0); err != nil {
		return RefCounter{}, err
	}
	// Initialise the counts for all sectors with a high enough value, so they
	// won't be deleted until we run a sweep and determine the actual count.
	// Note that we're using uint16 counters and those are 2 bytes each.
	zeroCounts := make([]uint16, numSectors, numSectors)
	for i := int64(0); i < numSectors; i++ {
		zeroCounts[i] = 1024
	}
	zeroBytes := make([]byte, numSectors*2, numSectors*2)
	for i := range zeroCounts {
		binary.LittleEndian.PutUint16(zeroBytes[i*2:i*2+2], zeroCounts[i])
	}
	if _, err = countsSection.WriteAt(zeroBytes, 0); err != nil {
		return RefCounter{}, err
	}
	if err := f.Sync(); err != nil {
		return RefCounter{}, err
	}
	return RefCounter{
		RefCounterHeader: h,
		sectorCounts:     zeroCounts,
		headerFS:         headerSection,
		countsFS:         countsSection,
	}, nil
}

// LoadRefCounter loads a refcounter from disk
func LoadRefCounter(path string) (RefCounter, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return RefCounter{}, err
	}
	defer func() {
		if err != nil {
			f.Close()
		}
	}()

	// TODO: Implement
	// headerSection := newFileSection(f, 0, RefCounterHeaderSize)
	// countsSection := newFileSection(f, RefCounterHeaderSize, remainingFile)

	// header, err := loadSafeContractHeader(f, int(RefCounterHeaderSize))
	// if err != nil {
	// 	return RefCounter{}, errors.AddContext(err, "unable to load refcounter header")
	// }

	return RefCounter{}, nil
}

// DeleteRefCounter deletes the counter's file from disk
// TODO: should the refcounter know where its file is? FileContractID?
func (rc *RefCounter) DeleteRefCounter(path string) (err error) {
	_, err = os.Stat(path)
	if err != nil {
		return
	}
	err = errors.Compose(rc.headerFS.Close(), rc.countsFS.Close(), os.Remove(path))
	return
}
