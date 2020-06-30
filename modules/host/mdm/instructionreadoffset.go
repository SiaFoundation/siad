package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instructionReadOffset is an instruction which reads from an offset within the
// file contract.
type instructionReadOffset struct {
	commonInstruction

	lengthOffset uint64
	offsetOffset uint64
}

// staticDecodeReadOffsetInstruction creates a new 'ReadOffset' instruction from
// the provided generic instruction.
func (p *program) staticDecodeReadOffsetInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadOffset {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadOffset, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadOffsetLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIReadOffsetLen, len(instruction.Args))
	}
	// Read args.
	offsetOffset := binary.LittleEndian.Uint64(instruction.Args[0:8])
	lengthOffset := binary.LittleEndian.Uint64(instruction.Args[8:16])
	return &instructionReadOffset{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: instruction.Args[16] == 1,
			staticState:       p.staticProgramState,
		},
		lengthOffset: lengthOffset,
		offsetOffset: offsetOffset,
	}, nil
}

// Execute executes the 'ReadOffset' instruction.
func (i *instructionReadOffset) Execute(previousOutput output) output {
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return errOutput(err)
	}
	offset, err := i.staticData.Uint64(i.offsetOffset)
	if err != nil {
		return errOutput(err)
	}
	// Translate the offset to a root.
	relOffset, secIdx, err := i.staticState.sectors.translateOffset(offset)
	if err != nil {
		return errOutput(err)
	}
	sectorRoot := i.staticState.sectors.merkleRoots[secIdx]

	// Execute it like a ReadSector instruction.
	output, fullSec := executeReadSector(previousOutput, i.staticState, length, relOffset, sectorRoot, i.staticMerkleProof)
	if !i.staticMerkleProof || output.Error != nil {
		return output
	}

	// Extend the proof.
	sectorProof := crypto.MerkleSectorRangeProof(i.staticState.sectors.merkleRoots, int(secIdx), int(secIdx+1))

	// If the segmentProof is empty, a full sector was downloaded. Then the
	// sectorProof is enough and we are done.
	if len(output.Proof) == 0 {
		output.Proof = sectorProof
		return output
	}

	// Otherwise we need to create a mixed range proof.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	mixedProof := crypto.MerkleMixedRangeProof(sectorProof, fullSec, int(modules.SectorSize), proofStart, proofEnd)
	output.Proof = mixedProof
	return output
}

// Collateral is zero for the ReadSector instruction.
func (i *instructionReadOffset) Collateral() types.Currency {
	return modules.MDMReadCollateral()
}

// Cost returns the cost of a ReadSector instruction.
func (i *instructionReadOffset) Cost() (executionCost, _ types.Currency, err error) {
	var length uint64
	length, err = i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return
	}
	executionCost = modules.MDMReadCost(i.staticState.priceTable, length)
	return
}

// Memory returns the memory allocated by the 'ReadSector' instruction beyond
// the lifetime of the instruction.
func (i *instructionReadOffset) Memory() uint64 {
	return modules.MDMReadMemory()
}

// Time returns the execution time of a 'ReadSector' instruction.
func (i *instructionReadOffset) Time() (uint64, error) {
	return modules.MDMTimeReadSector, nil
}
