package mdm

import (
	"bytes"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

const (
	// Initial contract size is 10 sectors.
	initialContractSectors = 10
)

// TestAppendSector tests appending a single sector to the program cache.
func TestAppendSector(t *testing.T) {
	// Initialize the sectors.
	sectorRoots := randomSectorRoots(initialContractSectors)
	s := newSectors(sectorRoots)
	newSectorData := randomSectorData()
	newSector := crypto.MerkleRoot(newSectorData)

	// Try appending an invalid sector -- should fail.
	_, err := s.appendSector([]byte{0})
	if err == nil {
		t.Fatal("expected error when appending an invalid sector")
	}

	// Append sector.
	newMerkleRoot, err := s.appendSector(newSectorData)
	if err != nil {
		t.Fatal(err)
	}

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

	// Drop the last sector and append it again.
	_, err = s.dropSectors(1)
	if err != nil {
		t.Fatal(err)
	}
	newMerkleRoot, err = s.appendSector(newSectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Check the return value.
	if merkleRoot != newMerkleRoot {
		t.Fatalf("expected merkle root %v but was %v", merkleRoot, newMerkleRoot)
	}

	// Check that the program cache is correct.
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

	// Append a sector and then drop it.
	newSectorData = randomSectorData()
	newSector = crypto.MerkleRoot(newSectorData)
	newMerkleRoot, err = s.appendSector(newSectorData)
	if err != nil {
		t.Fatal(err)
	}

	// Calculate expected roots.
	sectorRoots = append(sectorRoots, newSector)
	merkleRoot = cachedMerkleRoot(sectorRoots)

	// Check the return value.
	if merkleRoot != newMerkleRoot {
		t.Fatalf("expected merkle root %v but was %v", merkleRoot, newMerkleRoot)
	}

	_, err = s.dropSectors(1)
	if err != nil {
		t.Fatal(err)
	}

	sectorRoots = sectorRoots[:len(sectorRoots)-1]

	// Check that the program cache hasn't changed.
	if len(s.sectorsRemoved) > 0 {
		t.Fatalf("expected sectors removed length to be %v but was %v", 0, len(s.sectorsRemoved))
	}
	if len(s.sectorsGained) != 1 {
		t.Fatalf("expected sectors gained length to be %v but was %v", 1, len(s.sectorsGained))
	}
	if !reflect.DeepEqual(sectorRoots, s.merkleRoots) {
		t.Fatalf("expected sector roots different than actual sector roots")
	}
}

// TestDropSectors tests dropping sectors from the cache.
func TestDropSectors(t *testing.T) {
	// Initialize the sectors.
	sectorRoots := randomSectorRoots(initialContractSectors)
	s := newSectors(sectorRoots)

	// Try dropping zero sectors.
	root, err := s.dropSectors(0)
	if err != nil {
		t.Fatal(err)
	}
	if root != cachedMerkleRoot(sectorRoots) {
		t.Fatalf("unexpected merkle root")
	}
	if len(s.merkleRoots) != initialContractSectors {
		t.Fatalf("expected sectors length after dropping to be %v but was %v", initialContractSectors, len(s.merkleRoots))
	}

	// Try dropping half the sectors.
	root, err = s.dropSectors(5)
	if err != nil {
		t.Fatal(err)
	}
	if root != cachedMerkleRoot(sectorRoots[:5]) {
		t.Fatalf("unexpected merkle root")
	}
	if len(s.merkleRoots) != 5 {
		t.Fatalf("expected sectors length after dropping to be %v but was %v", 5, len(s.merkleRoots))
	}

	// Try dropping all remaining sectors.
	root, err = s.dropSectors(5)
	if err != nil {
		t.Fatal(err)
	}
	if root != cachedMerkleRoot([]crypto.Hash{}) {
		t.Fatalf("unexpected merkle root")
	}
	if len(s.merkleRoots) != 0 {
		t.Fatalf("expected sectors length after dropping to be %v but was %v", 0, len(s.merkleRoots))
	}

	// Try dropping some more sectors -- should fail.
	_, err = s.dropSectors(5)
	if err == nil {
		t.Fatal("expected error when dropping too many sectors")
	}
}

// TestHasSector tests checking if a sector exists in the cache or host.
func TestHasSector(t *testing.T) {
	// Initialize the sectors.
	sectorRoots := randomSectorRoots(initialContractSectors)
	s := newSectors(sectorRoots)

	// Each sector should exist.
	for _, root := range sectorRoots {
		if !s.hasSector(root) {
			t.Fatalf("sector %v not found in program cache", root)
		}
	}

	// These sectors should not exist.
	for i := 0; i < initialContractSectors; i++ {
		root := randomSector()
		if s.hasSector(root) {
			t.Fatalf("sector %v should not be in program cache or host", root)
		}
	}
}

// TestReadSector tests reading sector data from the cache and host.
func TestReadSector(t *testing.T) {
	// Initialize the host and sectors.
	sectorRoots := randomSectorRoots(initialContractSectors)
	host := newTestHost()
	host.sectors = randomSectorMap(sectorRoots)
	sectorsGained := randomSectorRoots(initialContractSectors)
	sectorRoots = append(sectorRoots, sectorsGained...)
	sectorsGainedMap := randomSectorMap(sectorsGained)
	s := newSectors(sectorRoots)
	s.sectorsGained = sectorsGainedMap

	// Read data for each existing sector.
	for _, root := range sectorRoots[:initialContractSectors] {
		data, err := s.readSector(host, root)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(host.sectors[root], data) {
			t.Fatalf("root %v not found in host", root)
		}
	}
	for _, root := range sectorRoots[initialContractSectors:] {
		data, err := s.readSector(host, root)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(sectorsGainedMap[root], data) {
			t.Fatalf("root %v not found in cache", root)
		}
	}

	// These sectors should not exist.
	for i := 0; i < initialContractSectors; i++ {
		root := randomSector()
		if _, err := s.readSector(host, root); err == nil {
			t.Fatalf("found a root %v which shouldn't exist", root)
		}
	}
}
