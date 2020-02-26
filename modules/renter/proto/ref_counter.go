package proto

import "gitlab.com/NebulousLabs/Sia/types"

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

		// Holds the number of references to that sector. We use 2 bytes per sector.
		SectorCounts []byte
	}

	// RefCounterHeader contains metadata about the reference counter file
	RefCounterHeader struct {
		Version                 [8]byte
		ID                      types.FileContractID
		GarbageCollectionOffset uint64
		NumSectors              uint64 // total number of sectors available under this contract, including garbage
	}
)

var (
	// RefCounterHeaderSize is the size of the header in bytes
	RefCounterHeaderSize = int64(56)

	// RefCounterVersion defines the latest version of the RefCounter
	// TODO: How do we actually do versioning? Check other structs.
	RefCounterVersion = [8]byte{0, 0, 0, 0, 0, 0, 0, 1}
)

// TODO: Add update utility (contract.go:147)
