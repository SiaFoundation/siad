package proto

import (
	"encoding/binary"
	"math"
	"sync"

	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

var (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = int64(56)

	// RefCounterVersion defines the latest version of the RefCounter
	// TODO: How do we actually do versioning? Check other structs.
	RefCounterVersion = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}

	// ErrInvalidSectorNumber is thrown when the requested sector number ∉ [0, NumSectors)
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
		sectorCounts []byte

		headerFS *fileSection
		countsFS *fileSection
		mu       sync.Mutex
	}

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		Version       [8]byte
		ID            types.FileContractID
		GarbageOffset int64
		NumSectors    int64 // total number of sectors available under this contract, including garbage
	}
)

// IncrementCount increments the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the oupdated number of references or an error.
func (rc *RefCounter) IncrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= rc.GarbageOffset {
		return 0, ErrInvalidSectorNumber
	}
	off := secNum * 2
	c := binary.LittleEndian.Uint16(rc.sectorCounts[off : off+2])
	if c == math.MaxUint16 {
		// TODO: Handle the overflow!
	}
	c++
	binary.LittleEndian.PutUint16(rc.sectorCounts[off:off+2], c)
	rc.countsFS.WriteAt(rc.sectorCounts[off:off+2], off)
	return c, nil
}

// DecrementCount decrements the reference counter of a given sector. The sector
// is specified by its sequential number (`secNum`).
// Returns the oupdated number of references or an error.
func (rc *RefCounter) DecrementCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= rc.GarbageOffset {
		return 0, ErrInvalidSectorNumber
	}
	off := secNum * 2
	c := binary.LittleEndian.Uint16(rc.sectorCounts[off : off+2])
	c--
	if c <= 0 { // handle the odd case where c might already be zero
		rc.managedMarkSectorAsGarbage(secNum)
		return 0, nil
	}
	binary.LittleEndian.PutUint16(rc.sectorCounts[off:off+2], c)
	rc.countsFS.WriteAt(rc.sectorCounts[off:off+2], off)
	return c, nil
}

// GetCount returns the number of references to the given sector
// Note: Use a pointer because a gigabyte file has >512B RefCounter object.
func (rc *RefCounter) GetCount(secNum int64) (uint16, error) {
	if secNum < 0 || secNum >= rc.GarbageOffset {
		return 0, ErrInvalidSectorNumber
	}
	off := 2 * secNum
	c := binary.LittleEndian.Uint16(rc.sectorCounts[off : off+2])
	return c, nil
}

func (rc *RefCounter) managedMarkSectorAsGarbage(secNum int64) {
	// this method is unexported and the secNum validation is already done.
	off := 2 * secNum
	// TODO: get the contract, swap the sectors, recalculate the merkle root (?)
	// and then execute the code below
	rc.mu.Lock()
	copy(rc.sectorCounts[off:off+2], rc.sectorCounts[rc.GarbageOffset:rc.GarbageOffset+2])
	rc.GarbageOffset -= 2
	rc.mu.Unlock()
}
