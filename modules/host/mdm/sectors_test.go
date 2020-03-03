package mdm

import (
	"bytes"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// randomSector is a testing helper function that initializes a random sector.
func randomSector() crypto.Hash {
	var sector crypto.Hash
	fastrand.Read(sector[:])
	return sector
}

// randomSectorData is a testing helper function that initializes random sector
// data.
func randomSectorData() []byte {
	return fastrand.Bytes(int(modules.SectorSize))
}

// randomSectorRoots is a testing helper function that initializes a number of
// random sector roots.
func randomSectorRoots(numRoots int) []crypto.Hash {
	roots := make([]crypto.Hash, 10)
	for i := 0; i < 10; i++ { // initial contract size is 10 sectors.
		fastrand.Read(roots[i][:]) // random initial merkle root
	}
	return roots
}

// randomSectorMap is a testing helper function that initializes a map with
// random sector data.
func randomSectorMap(roots []crypto.Hash) map[crypto.Hash][]byte {
	rootMap := make(map[crypto.Hash][]byte)
	for _, root := range roots {
		rootMap[root] = randomSectorData()
	}
	return rootMap
}

// TestAppendSector tests appending a single sector to the program cache.
func TestAppendSector(t *testing.T) {
	// Initialize the sectors.
	sectorRoots := randomSectorRoots(10)
	s := newSectors(sectorRoots)
	newSectorData := randomSectorData()
	newSector := crypto.MerkleRoot(newSectorData)

	// Append sector.
	newMerkleRoot := s.appendSector(newSectorData)

	// Calculate expected roots.
	sectorRoots = append(sectorRoots, newSector)
	merkleRoot := cachedMerkleRoot(sectorRoots)

	// Check the return value.
	if merkleRoot != newMerkleRoot {
		t.Fatalf("expected merkle root %v but was %v", merkleRoot, newMerkleRoot)
	}

	// Check each field of `sectors`.
	if len(s.sectorsRemoved) > 0 {
		t.Fatalf("expected sectors removed length to be %v but was %v", 0, len(s.sectorsRemoved))
	}
	if len(s.sectorsGained) != 1 {
		t.Fatalf("expected sectors gained length to be %v but was %v", 1, len(s.sectorsGained))
	}
	if !bytes.Equal(s.sectorsGained[newSector], newSectorData) {
		t.Fatalf("new sector not found in sectors gained")
	}
	if !reflect.DeepEqual(sectorRoots, s.merkleRoots) {
		t.Fatalf("expected sector roots different than actual sector roots")
	}
}

// TestHasSector tests checking if a sector exists in the cache or host.
func TestHasSector(t *testing.T) {
	// Initialize the sectors.
	sectorRoots := randomSectorRoots(10)
	s := newSectors(sectorRoots)

	// Each sector should exist.
	for _, root := range sectorRoots {
		if !s.hasSector(root) {
			t.Fatalf("sector %v not found in program cache", root)
		}
	}

	// These sectors should not exist.
	for i := 0; i < 10; i++ {
		root := randomSector()
		if s.hasSector(root) {
			t.Fatalf("sector %v should not be in program cache or host", root)
		}
	}
}

// TestReadSector tests reading sector data from the cache and host.
func TestReadSector(t *testing.T) {
	// Initialize the host and sectors.
	sectorRoots := randomSectorRoots(10)
	host := newTestHost()
	host.sectors = randomSectorMap(sectorRoots)
	sectorsGained := randomSectorRoots(10)
	sectorRoots = append(sectorRoots, sectorsGained...)
	sectorsGainedMap := randomSectorMap(sectorsGained)
	s := newSectors(sectorRoots)
	s.sectorsGained = sectorsGainedMap

	// Read data for each existing sector.
	for _, root := range sectorRoots[:10] {
		data, err := s.readSector(host, root)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(host.sectors[root], data) {
			t.Fatalf("root %v not found in host", root)
		}
	}
	for _, root := range sectorRoots[10:] {
		data, err := s.readSector(host, root)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(sectorsGainedMap[root], data) {
			t.Fatalf("root %v not found in cache", root)
		}
	}

	// These sectors should not exist.
	for i := 0; i < 10; i++ {
		root := randomSector()
		if _, err := s.readSector(host, root); err == nil {
			t.Fatalf("found a root %v which shouldn't exist", root)
		}
	}
}
