package mdm

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// errSectorNotFound return by ReadSector if the sector can't be found.
var errSectorNotFound = errors.New("sector not found")

type (
	// TestHost is a dummy host for testing which satisfies the Host interface.
	TestHost struct {
		generateSectors bool
		blockHeight     types.BlockHeight
		sectors         map[crypto.Hash][]byte
		registry        map[modules.RegistryEntryID]TestRegistryValue
		mu              sync.Mutex
	}
	TestRegistryValue struct {
		modules.SignedRegistryValue
		spk       types.SiaPublicKey
		entryType modules.RegistryEntryType
	}
	// TestStorageObligation is a dummy storage obligation for testing which
	// satisfies the StorageObligation interface.
	TestStorageObligation struct {
		host        *TestHost
		sectorMap   map[crypto.Hash][]byte
		sectorRoots []crypto.Hash

		// contract related fields.
		sk crypto.SecretKey
	}
)

func newTestHost() *TestHost {
	return newCustomTestHost(true)
}

func newCustomTestHost(generateSectors bool) *TestHost {
	return &TestHost{
		generateSectors: generateSectors,
		registry:        make(map[modules.RegistryEntryID]TestRegistryValue),
		sectors:         make(map[crypto.Hash][]byte),
	}
}

func (h *TestHost) newTestStorageObligation(locked bool) *TestStorageObligation {
	sk, _ := crypto.GenerateKeyPair()
	return &TestStorageObligation{
		host:      h,
		sectorMap: make(map[crypto.Hash][]byte),
		sk:        sk,
	}
}

// BlockHeight returns an incremented blockheight.
func (h *TestHost) BlockHeight() types.BlockHeight {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.blockHeight++
	return h.blockHeight
}

// HasSector indicates whether the host stores a sector with a given root or
// not.
func (h *TestHost) HasSector(sectorRoot crypto.Hash) bool {
	h.mu.Lock()
	_, exists := h.sectors[sectorRoot]
	h.mu.Unlock()
	return exists
}

// RegistryGet retrieves a value from the registry.
func (h *TestHost) RegistryGet(sid modules.RegistryEntryID) (types.SiaPublicKey, modules.SignedRegistryValue, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, exists := h.registry[sid]
	if !exists {
		return types.SiaPublicKey{}, modules.SignedRegistryValue{}, false
	}
	return v.spk, v.SignedRegistryValue, true
}

// RegistryUpdate updates a value in the registry.
func (h *TestHost) RegistryUpdate(rv modules.SignedRegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (modules.SignedRegistryValue, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := modules.DeriveRegistryEntryID(pubKey, rv.Tweak)
	oldRV, exists := h.registry[key]

	if exists && rv.Revision < oldRV.Revision {
		return oldRV.SignedRegistryValue, modules.ErrLowerRevNum
	}
	if exists && rv.Revision == oldRV.Revision {
		return oldRV.SignedRegistryValue, modules.ErrSameRevNum
	}

	h.registry[key] = TestRegistryValue{
		entryType:           rv.Type,
		SignedRegistryValue: rv,
		spk:                 pubKey,
	}
	return oldRV.SignedRegistryValue, nil
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

// AddRandomSector adds a random sector to the obligation and corresponding
// host.
func (so *TestStorageObligation) AddRandomSector() {
	data := fastrand.Bytes(int(modules.SectorSize))
	root := crypto.MerkleRoot(data)
	so.host.sectors[root] = data
	so.sectorRoots = append(so.sectorRoots, root)
}

// AddRandomSectors adds n random sectors to the obligation and corresponding
// host.
func (so *TestStorageObligation) AddRandomSectors(n int) {
	for i := 0; i < n; i++ {
		so.AddRandomSector()
	}
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

// RecentRevision implements the StorageObligation interface.
func (so *TestStorageObligation) RecentRevision() types.FileContractRevision {
	return types.FileContractRevision{
		NewFileMerkleRoot: so.MerkleRoot(),
		NewFileSize:       so.ContractSize(),
	}
}

// RevisionTxn returns the revision transaction for the obligation including a
// renter sig.
func (so *TestStorageObligation) RevisionTxn() types.Transaction {
	revTxn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{
			so.RecentRevision(),
		},
		TransactionSignatures: []types.TransactionSignature{
			{
				ParentID:       crypto.Hash(types.FileContractID{}),
				PublicKeyIndex: 0,
				CoveredFields: types.CoveredFields{
					FileContractRevisions: []uint64{0},
				},
			},
		},
	}
	hash := revTxn.SigHash(0, so.host.BlockHeight())
	sig := crypto.SignHash(hash, so.sk)
	revTxn.TransactionSignatures[0].Signature = sig[:]
	return revTxn
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
		Validity: time.Minute,

		AccountBalanceCost:   types.NewCurrency64(1),
		FundAccountCost:      types.NewCurrency64(1),
		UpdatePriceTableCost: types.NewCurrency64(1),
		InitBaseCost:         types.NewCurrency64(1),
		LatestRevisionCost:   types.NewCurrency64(1),
		MemoryTimeCost:       types.NewCurrency64(1),
		CollateralCost:       types.NewCurrency64(1),

		// Instruction costs
		DropSectorsBaseCost: types.NewCurrency64(1),
		DropSectorsUnitCost: types.NewCurrency64(1),
		HasSectorBaseCost:   types.NewCurrency64(1),
		ReadBaseCost:        types.NewCurrency64(1),
		ReadLengthCost:      types.NewCurrency64(1),
		SwapSectorCost:      types.NewCurrency64(1),
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
func (mdm *MDM) ExecuteProgramWithBuilder(tb *testProgramBuilder, so *TestStorageObligation, duration types.BlockHeight, finalized bool) ([]Output, error) {
	outputs, budget, err := mdm.ExecuteProgramWithBuilderCustomBudget(tb, so, duration, finalized)
	if err != nil {
		return nil, err
	}
	// Budget should be drained now.
	if !budget.Remaining().IsZero() {
		return nil, fmt.Errorf("remaining budget should be empty but was %v", budget.Remaining())
	}
	return outputs, nil
}

// ExecuteProgramWithBuilderCustomBudget is a convenience wrapper around
// mdm.ExecuteProgram. It runs the program constructed by tb with the storage
// obligation so. It will also return the outputs as a slice for convenience.
func (mdm *MDM) ExecuteProgramWithBuilderCustomBudget(tb *testProgramBuilder, so *TestStorageObligation, duration types.BlockHeight, finalized bool) ([]Output, *modules.RPCBudget, error) {
	// Execute the program.
	finalize, budget, outputs, err := mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, duration, finalized)
	if err != nil {
		return nil, nil, err
	}
	// Finalize the program if finalized is true.
	if finalize == nil && finalized {
		return nil, nil, errors.New("finalize method was 'nil' but finalized was 'true'")
	} else if finalized {
		if err = finalize(so); err != nil {
			return nil, nil, err
		}
	}
	return outputs, budget, nil
}

// ExecuteProgramWithBuilderManualFinalize is a convenience wrapper around
// mdm.ExecuteProgram. It runs the program constructed by tb with the storage
// obligation so. It will also return the outputs as a slice for convenience.
// Finalization needs to be done manually after running the program.
func (mdm *MDM) ExecuteProgramWithBuilderManualFinalize(tb *testProgramBuilder, so *TestStorageObligation, duration types.BlockHeight, finalized bool) (FnFinalize, *modules.RPCBudget, []Output, error) {
	ctx := context.Background()
	program, programData := tb.Program()
	values := tb.Cost()
	_, _, collateral, _ := values.Cost()
	budget := values.Budget(finalized)
	finalize, outputChan, err := mdm.ExecuteProgram(ctx, tb.staticPT, program, budget, collateral, so, duration, uint64(len(programData)), bytes.NewReader(programData))
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
func (o Output) assert(newSize uint64, newMerkleRoot crypto.Hash, proof []crypto.Hash, output []byte, err error) error {
	if err != nil && o.Error == nil {
		return fmt.Errorf("output didn't contain error")
	} else if err == nil && o.Error != nil {
		return fmt.Errorf("output contained error: %v", o.Error)
	} else if o.Error != nil && err != nil && o.Error.Error() != err.Error() {
		return fmt.Errorf("output errors don't match %v != %v", o.Error, err)
	}
	if o.NewSize != newSize {
		return fmt.Errorf("expected newSize %v but got %v", newSize, o.NewSize)
	}
	if o.NewMerkleRoot != newMerkleRoot {
		return fmt.Errorf("expected newMerkleRoot %v but got %v", newMerkleRoot, o.NewMerkleRoot)
	}
	if !bytes.Equal(o.Output, output) {
		return fmt.Errorf("expected output %v\n but got %v", output, o.Output)
	}
	if len(o.Proof)+len(proof) != 0 && !reflect.DeepEqual(o.Proof, proof) {
		return fmt.Errorf("expected proof %v but got %v", proof, o.Proof)
	}
	return nil
}
