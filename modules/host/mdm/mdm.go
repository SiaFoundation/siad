package mdm

import (
	"gitlab.com/NebulousLabs/threadgroup"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// StorageObligation defines an interface the storage obligation must adhere to.
type StorageObligation interface {
	// Update updates the storage obligation.
	Update(sectorRoots []crypto.Hash, sectorsRemoved map[crypto.Hash]struct{}, sectorsGained map[crypto.Hash][]byte) error
}

// StorageObligationSnapshot defines an interface the snapshot must adhere to in
// order for the mdm to be able to execute a program.
type StorageObligationSnapshot interface {
	// ContractSize returns the current contract size of the storage obligation.
	ContractSize() uint64
	// MerkleRoot returns the filecontract's current root.
	MerkleRoot() crypto.Hash
	// RecentRevision returns the recent revision at the time the snapshot was
	// taken.
	RecentRevision() types.FileContractRevision
	// RevisionTxn returns the recent revision txn of the so.
	RevisionTxn() types.Transaction
	// SectorRoots returns the roots of the storage obligation.
	SectorRoots() []crypto.Hash
}

// Host defines the minimal interface a Host needs to
// implement to be used by the mdm.
type Host interface {
	BlockHeight() types.BlockHeight
	HasSector(crypto.Hash) bool
	ReadSector(sectorRoot crypto.Hash) ([]byte, error)
	RegistryUpdate(rv modules.SignedRegistryValue, pubKey types.SiaPublicKey, expiry types.BlockHeight) (modules.SignedRegistryValue, error)
	RegistryGet(sid modules.RegistryEntryID) (types.SiaPublicKey, modules.SignedRegistryValue, bool)
}

// MDM (Merklized Data Machine) is a virtual machine that executes instructions
// on the data in a Sia file contract. The file contract tracks the size and
// Merkle root of the underlying data, which the MDM will update when running
// instructions that modify the file contract data. Each instruction can
// optionally produce a cryptographic proof that the instruction was executed
// honestly. Every instruction has an execution cost, and instructions are
// batched into atomic sets called 'programs' that are either entirely applied
// or are not applied at all.
type MDM struct {
	host Host
	tg   threadgroup.ThreadGroup
}

// New creates a new MDM.
func New(h Host) *MDM {
	return &MDM{
		host: h,
	}
}

// Stop will stop the MDM and wait for all of the spawned programs to stop
// executing while also preventing new programs from being started.
func (mdm *MDM) Stop() error {
	return mdm.tg.Stop()
}

// cachedMerkleRoot calculates the root of a set of sector roots.
func cachedMerkleRoot(roots []crypto.Hash) crypto.Hash {
	log2SectorSize := uint64(0)
	for 1<<log2SectorSize < (modules.SectorSize / crypto.SegmentSize) {
		log2SectorSize++
	}
	ct := crypto.NewCachedTree(log2SectorSize)
	for _, root := range roots {
		ct.PushSubTree(0, root)
	}
	return ct.Root()
}
