package mdm

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/threadgroup"
)

// StorageObligation defines the minimal interface a StorageObligation needs to
// implement to be used by the mdm.
type StorageObligation interface {
	Locked() bool
	Update(sectorsRemoved, sectorsGained []crypto.Hash, gainedSectorData [][]byte) error
}

// Host defines the minimal interface a Host needs to
// implement to be used by the mdm.
type Host interface {
	BlockHeight() types.BlockHeight
	ReadSector(sectorRoot crypto.Hash) ([]byte, error)
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
