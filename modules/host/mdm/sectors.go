package mdm

import (
	"fmt"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
)

// sectors contains the program cache, including gained and removed sectors as
// well as the list of sector roots.
type sectors struct {
	sectorsRemoved map[crypto.Hash]struct{}
	sectorsGained  map[crypto.Hash][]byte
	merkleRoots    []crypto.Hash
}

// newSectors creates a program cache given an initial list of sector roots.
func newSectors(roots []crypto.Hash) sectors {
	return sectors{
		sectorsRemoved: make(map[crypto.Hash]struct{}),
		sectorsGained:  make(map[crypto.Hash][]byte),
		merkleRoots:    roots,
	}
}

// appendSector adds the data to the program cache and returns the new merkle
// root.
func (s *sectors) appendSector(sectorData []byte) (crypto.Hash, error) {
	if uint64(len(sectorData)) != modules.SectorSize {
		return crypto.Hash{}, fmt.Errorf("trying to append data of length %v", len(sectorData))
	}
	newRoot := crypto.MerkleRoot(sectorData)

	// Update the program cache.
	_, removed := s.sectorsRemoved[newRoot]
	if removed {
		// If the sector has been marked as removed, unmark it.
		delete(s.sectorsRemoved, newRoot)
	} else {
		// Add the sector to the cache.
		s.sectorsGained[newRoot] = sectorData
	}

	// Update the roots.
	s.merkleRoots = append(s.merkleRoots, newRoot)

	// Return the new merkle root of the contract.
	return cachedMerkleRoot(s.merkleRoots), nil
}

// dropSectors drops the specified number of sectors and returns the new merkle
// root.
func (s *sectors) dropSectors(numSectorsDropped uint64) (crypto.Hash, error) {
	oldNumSectors := uint64(len(s.merkleRoots))
	if numSectorsDropped > oldNumSectors {
		return crypto.Hash{}, fmt.Errorf("trying to drop %v sectors which is more than the amount of sectors (%v)", numSectorsDropped, oldNumSectors)
	}
	newNumSectors := oldNumSectors - numSectorsDropped

	// Update the roots.
	droppedRoots := s.merkleRoots[newNumSectors:]
	s.merkleRoots = s.merkleRoots[:newNumSectors]

	// Update the program cache.
	for _, droppedRoot := range droppedRoots {
		_, gained := s.sectorsGained[droppedRoot]
		if gained {
			// Remove the sectors from the cache.
			delete(s.sectorsGained, droppedRoot)
		} else {
			// Mark the sectors as removed in the cache.
			s.sectorsRemoved[droppedRoot] = struct{}{}
		}
	}

	// Compute the new merkle root of the contract.
	return cachedMerkleRoot(s.merkleRoots), nil
}

// hasSector checks if the given root exists, first checking the program cache
// and then querying the host.
func (s *sectors) hasSector(sectorRoot crypto.Hash) bool {
	for _, root := range s.merkleRoots {
		if root == sectorRoot {
			return true
		}
	}
	return false
}

// swapSectors swaps the sectors at idx1 and idx2 and returns the new merkle
// root.
func (s *sectors) swapSectors(idx1, idx2 uint64) (crypto.Hash, error) {
	if idx1 >= uint64(len(s.merkleRoots)) {
		return crypto.Hash{}, fmt.Errorf("idx1 out-of-bounds: %v >= %v", idx1, len(s.merkleRoots))
	}
	if idx2 >= uint64(len(s.merkleRoots)) {
		return crypto.Hash{}, fmt.Errorf("idx2 out-of-bounds: %v >= %v", idx2, len(s.merkleRoots))
	}
	s.merkleRoots[idx1], s.merkleRoots[idx2] = s.merkleRoots[idx2], s.merkleRoots[idx1]
	return cachedMerkleRoot(s.merkleRoots), nil
}

// translateOffset translates an offset within a filecontract into a relative
// offset within a sector and the sector's index within the contract.
func (s *sectors) translateOffset(offset uint64) (uint64, uint64, error) {
	// Compute the sector offset.
	secOff := offset / modules.SectorSize
	relOff := offset % modules.SectorSize
	// Check for out of bounds.
	if uint64(len(s.merkleRoots)) <= secOff {
		return 0, 0, fmt.Errorf("translateOffset: secOff out of bounds %v >= %v", len(s.merkleRoots), secOff)
	}
	return relOff, secOff, nil
}

// readSector reads data from the given root, returning the entire sector.
func (s *sectors) readSector(host Host, sectorRoot crypto.Hash) ([]byte, error) {
	// The root exists. First check the gained sectors.
	if data, exists := s.sectorsGained[sectorRoot]; exists {
		return data, nil
	}

	// Check the host.
	return host.ReadSector(sectorRoot)
}
