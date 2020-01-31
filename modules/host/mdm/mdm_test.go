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

func newTestHost() Host {
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

// Update implements the StorageObligation interface
func (so *TestStorageObligation) Update(sectorRoots, sectorsRemoved, sectorsGained []crypto.Hash, gainedSectorData [][]byte) error {
	for _, removedSector := range sectorsRemoved {
		if _, exists := so.sectorMap[removedSector]; !exists {
			return errors.New("sector doesn't exist")
		}
		delete(so.sectorMap, removedSector)
	}
	for i, gainedSector := range sectorsGained {
		if _, exists := so.sectorMap[gainedSector]; exists {
			return errors.New("sector already exists")
		}
		so.sectorMap[gainedSector] = gainedSectorData[i]
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
