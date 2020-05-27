package mdm

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// errSectorNotFound return by ReadSector if the sector can't be found.
var errSectorNotFound = errors.New("sector not found")

type (
	// TestHost is a dummy host for testing which satisfies the Host interface.
	TestHost struct {
		generateSectors bool
		blockHeight     types.BlockHeight
		sectors         map[crypto.Hash][]byte
		mu              sync.Mutex
	}
	// TestStorageObligation is a dummy storage obligation for testing which
	// satisfies the StorageObligation interface.
	TestStorageObligation struct {
		sectorMap   map[crypto.Hash][]byte
		sectorRoots []crypto.Hash
	}
)

func newTestHost() *TestHost {
	return newCustomTestHost(true)
}

func newCustomTestHost(generateSectors bool) *TestHost {
	return &TestHost{
		generateSectors: generateSectors,
		sectors:         make(map[crypto.Hash][]byte),
	}
}

func newTestStorageObligation(locked bool) *TestStorageObligation {
	return &TestStorageObligation{
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
func (h *TestHost) HasSector(sectorRoot crypto.Hash) bool {
	_, exists := h.sectors[sectorRoot]
	return exists
}

// ReadSector implements the Host interface by returning a random sector for
// each root. Calling ReadSector multiple times on the same root will result in
// the same data.
func (h *TestHost) ReadSector(sectorRoot crypto.Hash) ([]byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	data, exists := h.sectors[sectorRoot]
	if !exists && h.generateSectors {
		data = fastrand.Bytes(int(modules.SectorSize))
		h.sectors[sectorRoot] = data
	} else if !exists && !h.generateSectors {
		return nil, errSectorNotFound
	}
	return data, nil
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
func (so *TestStorageObligation) Update(sectorRoots []crypto.Hash, sectorsRemoved map[crypto.Hash]struct{}, sectorsGained map[crypto.Hash][]byte) error {
	for removedSector := range sectorsRemoved {
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
func newTestPriceTable() *modules.RPCPriceTable {
	return &modules.RPCPriceTable{
		Expiry:               time.Now().Add(time.Minute).Unix(),
		UpdatePriceTableCost: types.NewCurrency64(1),
		InitBaseCost:         types.NewCurrency64(1),
		MemoryTimeCost:       types.NewCurrency64(1),
		CollateralCost:       types.NewCurrency64(1),

		// Instruction costs
		DropSectorsBaseCost: types.NewCurrency64(1),
		DropSectorsUnitCost: types.NewCurrency64(1),
		HasSectorBaseCost:   types.NewCurrency64(1),
		ReadBaseCost:        types.NewCurrency64(1),
		ReadLengthCost:      types.NewCurrency64(1),
		WriteBaseCost:       types.NewCurrency64(1),
		WriteLengthCost:     types.NewCurrency64(1),
		WriteStoreCost:      types.NewCurrency64(1),

		// Bandwidth costs
		DownloadBandwidthCost: types.NewCurrency64(1),
		UploadBandwidthCost:   types.NewCurrency64(1),
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

// ExecuteProgramWithBuilder is a convenience wrapper around mdm.ExecuteProgram.
// It runs the program constructed by tb with the storage obligation so. It will
// also return the outputs as a slice for convenience.
func (mdm *MDM) ExecuteProgramWithBuilder(tb *testProgramBuilder, so *TestStorageObligation, finalized bool) ([]Output, error) {
	// Execute the program.
	finalize, budget, outputs, err := mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, finalized)
	if err != nil {
		return nil, err
	}
	// Finalize the program if finalized is true.
	if finalize == nil && finalized {
		return nil, errors.New("finalize method was 'nil' but finalized was 'true'")
	} else if finalized {
		if err = finalize(so); err != nil {
			return nil, err
		}
	}
	// Budget should be drained now.
	if !budget.Remaining().IsZero() {
		return nil, fmt.Errorf("remaining budget should be empty but was %v", budget.Remaining())
	}
	return outputs, nil
}

// ExecuteProgramWithBuilderManualFinalize is a convenience wrapper around
// mdm.ExecuteProgram. It runs the program constructed by tb with the storage
// obligation so. It will also return the outputs as a slice for convenience.
// Finalization needs to be done manually after running the program.
func (mdm *MDM) ExecuteProgramWithBuilderManualFinalize(tb *testProgramBuilder, so *TestStorageObligation, finalized bool) (FnFinalize, *modules.RPCBudget, []Output, error) {
	ctx := context.Background()
	program, programData := tb.Program()
	values := tb.Cost()
	_, _, collateral := values.Cost()
	budget := values.Budget(finalized)
	finalize, outputChan, err := mdm.ExecuteProgram(ctx, tb.staticPT, program, budget, collateral, so, uint64(len(programData)), bytes.NewReader(programData))
	if err != nil {
		return nil, nil, nil, err
	}
	// Collect outputs
	var outputs []Output
	for output := range outputChan {
		outputs = append(outputs, output)
	}
	// Assert outputs
	err = values.AssertOutputs(outputs)
	if err != nil {
		return nil, nil, nil, err
	}
	return finalize, budget, outputs, nil
}

// assert asserts an output against the provided values. Costs should be
// asserted using the TestValues type or they will be asserted implicitly when
// using ExecuteProgramWithBuilder.
func (o Output) assert(newSize uint64, newMerkleRoot crypto.Hash, proof []crypto.Hash, output []byte) error {
	if o.NewSize != newSize {
		return fmt.Errorf("expected newSize %v but got %v", newSize, o.NewSize)
	}
	if o.NewMerkleRoot != newMerkleRoot {
		return fmt.Errorf("expected newMerkleRoot %v but got %v", newSize, o.NewMerkleRoot)
	}
	if len(o.Proof)+len(proof) != 0 && !reflect.DeepEqual(o.Proof, proof) {
		return fmt.Errorf("expected proof %v but got %v", proof, o.Proof)
	}
	if !bytes.Equal(o.Output, output) {
		return fmt.Errorf("expected o %v but got %v", o, o.Output)
	}
	return nil
}
