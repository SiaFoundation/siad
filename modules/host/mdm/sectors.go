package mdm

import (
	"errors"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// sectors contains the program cache, including gained and removed sectors as
// well as the list of sector roots.
type sectors struct {
	sectorsRemoved []crypto.Hash
	sectorsGained  map[crypto.Hash][]byte
	merkleRoots    []crypto.Hash
}

func newSectors(roots []crypto.Hash) sectors {
	return sectors{
		sectorsRemoved: make([]crypto.Hash, 0),
		sectorsGained:  make(map[crypto.Hash][]byte),
		merkleRoots:    roots,
	}
}

// appendSector adds the data to the program cache.
func (s *sectors) appendSector(sectorData []byte) crypto.Hash {
	newRoot := crypto.MerkleRoot(sectorData)

	s.sectorsGained[newRoot] = sectorData

	// Update the roots and compute the new merkle root of the contract.
	s.merkleRoots = append(s.merkleRoots, newRoot)
	newMerkleRoot := cachedMerkleRoot(s.merkleRoots)

	return newMerkleRoot
}

// hasSector checks if the given root exists, first checking the program cache
// and then querying the host.
func (s *sectors) hasSector(host Host, sectorRoot crypto.Hash) (bool, error) {
	if _, exists := s.sectorsGained[sectorRoot]; exists {
		return true, nil
	}

	return host.HasSector(sectorRoot)
}

// readSector reads data from the given root, returning the entire sector.
func (s *sectors) readSector(host Host, sectorRoot crypto.Hash) ([]byte, error) {
	// Check merkleRoots first -- otherwise the root wasn't added, or was deleted.
	inList := false
	for _, root := range s.merkleRoots {
		if root == sectorRoot {
			inList = true
			break
		}
	}
	if !inList {
		return nil, errors.New("root not found in list of roots")
	}

	// The root exists. First check the gained sectors.
	if data, exists := s.sectorsGained[sectorRoot]; exists {
		return data, nil
	}

	// Check the host.
	data, err := host.ReadSector(sectorRoot)
	if err != nil {
		return nil, err
	}
	return data, nil
}
