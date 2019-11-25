package mdm

import (
	"encoding/binary"
	"errors"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instruction is the interface an instruction needs to implement to be part of
// a program.
type instruction interface {
	Execute(fcRoot crypto.Hash, budget Cost) Output
	ReadOnly() bool
}

// Output is the type returned by all instructions when being executed.
type Output struct {
	// The error will be set to nil unless the instruction experienced an error
	// during execution. If the instruction did experience an error during
	// execution, the program will halt at this instruction and no changes will
	// be committed.
	//
	// The error will be prefixed by 'invalid' if the error resulted from an
	// invalid program, and the error will be prefixed by 'hosterr' if the error
	// resulted from a host error such as a disk failure.
	Error error

	// NewBudget is the budget remaining after executing the instruction.
	NewBudget Cost

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
	staticMerkleProof  bool
	staticContractSize uint64 // contract size before executing instruction
	staticFCID         types.FileContractID
	staticData         *ProgramData
	staticState        *programState
}

// outputFromError is a convenience function to wrap an error in an Output.
func outputFromError(err error) Output {
	return Output{
		Error: err,
	}
}

// ****************************************************************************
//
// The following sections contain the supportd transactions of the MDM.
//
// ****************************************************************************

type instructionReadSector struct {
	commonInstruction

	lengthOff     uint64
	offsetOff     uint64
	merkleRootOff uint64
}

// decodeReadSectorInstruction creates a new 'ReadSector' instructions from the
// provided operands and adds it to the program. This is only possible as long
// as the program hasn't begun execution yet.
func (p *Program) decodeReadSectorInstruction(instruction modules.Instruction) error {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadSector {
		return fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadSector, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadSectorLen {
		return fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIReadSectorLen, len(instruction.Args))
	}
	// Read args.
	rootOff := binary.LittleEndian.Uint64(instruction.Args[:8])
	offsetOff := binary.LittleEndian.Uint64(instruction.Args[8:16])
	lengthOff := binary.LittleEndian.Uint64(instruction.Args[16:24])
	p.instructions = append(p.instructions, &instructionReadSector{
		commonInstruction: commonInstruction{
			staticContractSize: p.finalContractSize,
			staticData:         p.staticData,
			staticMerkleProof:  instruction.Args[24] == 1,
			staticState:        p.staticProgramState,
		},
		lengthOff:     lengthOff,
		merkleRootOff: rootOff,
		offsetOff:     offsetOff,
	})
	return nil
}

// Cost returns the cost of executing this instruction.
func (i *instructionReadSector) Cost() Cost {
	return ReadSectorCost()
}

// Execute execute the 'Read' instruction.
func (i *instructionReadSector) Execute(fcRoot crypto.Hash, budget Cost) Output {
	// Subtract cost from budget beforehand.
	newBudget, ok := budget.Min(ReadSectorCost())
	if !ok {
		return outputFromError(ErrInsufficientBudget)
	}
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOff)
	if err != nil {
		return outputFromError(err)
	}
	offset, err := i.staticData.Uint64(i.offsetOff)
	if err != nil {
		return outputFromError(err)
	}
	sectorRoot, err := i.staticData.Hash(i.merkleRootOff)
	if err != nil {
		return outputFromError(err)
	}
	// Validate the request.
	switch {
	case offset+length > modules.SectorSize:
		err = errors.New("request is out of bounds")
	case length == 0:
		err = errors.New("length cannot be zero")
	case i.staticMerkleProof && (offset%crypto.SegmentSize != 0 || length%crypto.SegmentSize != 0):
		err = errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
	}
	if err != nil {
		return outputFromError(err)
	}

	// Fetch the requested data.
	sectorData, err := i.staticState.host.ReadSector(sectorRoot)
	if err != nil {
		return outputFromError(err)
	}
	data := sectorData[offset : offset+length]

	// Construct the Merkle proof, if requested.
	var proof []crypto.Hash
	if i.staticMerkleProof {
		proofStart := int(offset) / crypto.SegmentSize
		proofEnd := int(offset+length) / crypto.SegmentSize
		proof = crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	}

	// Return the output.
	return Output{
		NewBudget:     newBudget,
		NewSize:       i.staticContractSize, // size stays the same
		NewMerkleRoot: fcRoot,               // root stays the same
		Output:        data,
		Proof:         proof,
	}
}

// ReadOnly for the 'Read' instruction is 'true'.
func (i *instructionReadSector) ReadOnly() bool {
	return true
}

// ****************************************************************************
