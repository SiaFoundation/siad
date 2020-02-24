package mdm

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
)

// sectors contains the program cache, including gained and removed sectors as
// well as the running list of sector roots.
type sectors struct {
	sectorsRemoved   []crypto.Hash
	sectorsGained    []crypto.Hash
	gainedSectorData [][]byte
	merkleRoots      []crypto.Hash
}

// appendSector adds the data to the program cache.
func (s *sectors) appendSector(sectorData []byte) crypto.Hash {
	newRoot := crypto.MerkleRoot(sectorData)

	// Update the storage obligation.
	s.sectorsGained = append(s.sectorsGained, newRoot)
	s.gainedSectorData = append(s.gainedSectorData, sectorData)

	// Update the roots and compute the new merkle root of the contract.
	s.merkleRoots = append(s.merkleRoots, newRoot)
	newMerkleRoot := cachedMerkleRoot(s.merkleRoots)

	return newMerkleRoot
}

// hasSector checks if the given root exists, first checking the program cache
// and then querying the host.
func (s *sectors) hasSector(host Host, sectorRoot crypto.Hash) (bool, error) {
	for _, sector := range s.sectorsGained {
		if sector == sectorRoot {
			return true, nil
		}
	}

	hasSector, err := host.HasSector(sectorRoot)
	if err != nil {
		return false, err
	}
	return hasSector, nil
}

// readSector reads data from the given root, returning both the entire sector
// as well as the desired segment at the offset.
func (s *sectors) readSector(host Host, offset, length uint64, sectorRoot crypto.Hash) ([]byte, []byte, error) {
	sectorData, err := host.ReadSector(sectorRoot)
	if err != nil {
		return nil, nil, err
	}
	readData := sectorData[offset : offset+length]
	return sectorData, readData, nil
}
