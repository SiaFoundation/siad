package mdm

import (
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

type (
	// TestHost is a dummy host for testing which satisfies the Host interface.
	TestHost struct {
		blockHeight types.BlockHeight
		sectors     map[crypto.Hash][]byte
		mu          sync.Mutex
	}
	// TestStorageObligation is a dummy storage obligation for testing which
	// satisfies the StorageObligation interface.
	TestStorageObligation struct {
		sectorMap   map[crypto.Hash][]byte
		sectorRoots []crypto.Hash
		locked      bool
	}
)

func newTestHost() *TestHost {
	return &TestHost{
		sectors: make(map[crypto.Hash][]byte),
	}
}

func newTestStorageObligation(locked bool) *TestStorageObligation {
	return &TestStorageObligation{
		locked:    locked,
		sectorMap: make(map[crypto.Hash][]byte),
	}
}

// BlockHeight returns an incremented blockheight every time it's called.
func (h *TestHost) BlockHeight() types.BlockHeight {
	h.blockHeight++
	return h.blockHeight
}

// HasSector indicates whether the host stores a sector with a given root or
// not.
func (h *TestHost) HasSector(sectorRoot crypto.Hash) (bool, error) {
	_, exists := h.sectors[sectorRoot]
	return exists, nil
}

// ReadSector implements the Host interface by returning a random sector for
// each root. Calling ReadSector multiple times on the same root will result in
// the same data.
func (h *TestHost) ReadSector(sectorRoot crypto.Hash) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, exists := h.sectors[sectorRoot]
	if !exists {
		data = fastrand.Bytes(int(modules.SectorSize))
		h.sectors[sectorRoot] = data
	}
	return data, nil
}

// Locked implements the StorageObligation interface.
func (so *TestStorageObligation) Locked() bool {
	return so.locked
}

// ContractSize implements the StorageObligation interface.
func (so *TestStorageObligation) ContractSize() uint64 {
	return uint64(len(so.sectorRoots)) * modules.SectorSize
}

// MerkleRoot implements the StorageObligation interface.
func (so *TestStorageObligation) MerkleRoot() crypto.Hash {
	if len(so.sectorRoots) == 0 {
		return crypto.Hash{}
	}
	return cachedMerkleRoot(so.sectorRoots)
}

// SectorRoots implements the StorageObligation interface.
func (so *TestStorageObligation) SectorRoots() []crypto.Hash {
	return so.sectorRoots
}

// Update implements the StorageObligation interface.
func (so *TestStorageObligation) Update(sectorRoots, sectorsRemoved []crypto.Hash, sectorsGained map[crypto.Hash][]byte) error {
	for _, removedSector := range sectorsRemoved {
		if _, exists := so.sectorMap[removedSector]; !exists {
			return errors.New("sector doesn't exist")
		}
		delete(so.sectorMap, removedSector)
	}
	for gainedSector, gainedSectorData := range sectorsGained {
		if _, exists := so.sectorMap[gainedSector]; exists {
			return errors.New("sector already exists")
		}
		so.sectorMap[gainedSector] = gainedSectorData
	}
	so.sectorRoots = sectorRoots
	return nil
}

// newTestPriceTable returns a price table for testing that charges 1 Hasting
// for every operation/rpc.
func newTestPriceTable() modules.RPCPriceTable {
	return modules.RPCPriceTable{
		Expiry:               time.Now().Add(time.Minute).Unix(),
		UpdatePriceTableCost: types.SiacoinPrecision,
		InitBaseCost:         types.SiacoinPrecision,
		MemoryTimeCost:       types.SiacoinPrecision,
		ReadBaseCost:         types.SiacoinPrecision,
		ReadLengthCost:       types.SiacoinPrecision,
		WriteBaseCost:        types.SiacoinPrecision,
		WriteLengthCost:      types.SiacoinPrecision,
	}
}

// TestNew tests the New method to create a new MDM.
func TestNew(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Check fields.
	if mdm.host != host {
		t.Fatal("host wasn't set correctly")
	}
}
