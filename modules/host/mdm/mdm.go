package mdm

import "gitlab.com/NebulousLabs/Sia/crypto"

// StorageObligation defines the minimal interface a StorageObligation needs to
// implement to be used by the mdm.
type StorageObligation interface {
	Locked() bool
}

// Host defines the minimal interface a Host needs to
// implement to be used by the mdm.
type Host interface {
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
}
