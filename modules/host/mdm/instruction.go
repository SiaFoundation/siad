package mdm

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instruction is the interface an instruction needs to implement to be part of
// a program.
type instruction interface {
	Cost() (cost types.Currency, refund types.Currency, _ error)
	Execute(output) output
	ReadOnly() bool
}

// Output is the type of the outputs returned by a program run on the MDM.
type Output struct {
	output
	ExecutionCost   types.Currency
	PotentialRefund types.Currency
}

// output is the type returned by all instructions when being executed.
type output struct {
	// The error will be set to nil unless the instruction experienced an error
	// during execution. If the instruction did experience an error during
	// execution, the program will halt at this instruction and no changes will
	// be committed.
	//
	// The error will be prefixed by 'invalid' if the error resulted from an
	// invalid program, and the error will be prefixed by 'hosterr' if the error
	// resulted from a host error such as a disk failure.
	Error error

	// NewSize is the size of a file contract after the execution of an
	// instruction.
	NewSize uint64

	// NewMerkleRoot is the merkle root after the execution of an instruction.
	NewMerkleRoot crypto.Hash

	// The output will be set to nil unless the instruction produces output for
	// the caller. One example of such an instruction would be 'Read()'. If
	// there was an error during execution, the output will be nil.
	Output []byte

	// The proof will be set to nil if there was an error, and also if no proof
	// was requested by the caller. Using only the proof, the caller will be
	// able to compute the next Merkle root and size of the contract.
	Proof []crypto.Hash
}

// commonInstruction contains all the fields shared by every instruction.
type commonInstruction struct {
	staticMerkleProof bool
	staticData        *programData
	staticState       *programState
}

// errOutput returns an instruction output that contains the specified error.
func errOutput(err error) output {
	return output{
		Error: err,
	}
}
