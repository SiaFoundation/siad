package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
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

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionReadOffset) Batch() bool {
	return false
}

// Execute executes the 'ReadOffset' instruction.
func (i *instructionReadOffset) Execute(previousOutput output) (output, types.Currency) {
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	offset, err := i.staticData.Uint64(i.offsetOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	// Translate the offset to a root.
	relOffset, secIdx, err := i.staticState.sectors.translateOffset(offset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	sectorRoot := i.staticState.sectors.merkleRoots[secIdx]

	// Execute it like a ReadSector instruction but without a proof since we
	// will add that manually later.
	output, fullSec := executeReadSector(previousOutput, i.staticState, length, relOffset, sectorRoot, false)
	if !i.staticMerkleProof || output.Error != nil {
		return output, types.ZeroCurrency
	}

	// Compute the proof range.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize

	// Create the proof.
	if length == modules.SectorSize {
		// If a full sector was downloaded, we don't need to pass in the data
		// but instead pass in all roots.
		sectorHashes := i.staticState.sectors.merkleRoots
		output.Proof = crypto.MerkleMixedRangeProof(sectorHashes, nil, int(modules.SectorSize), proofStart, proofEnd)
	} else {
		// If a partial sector was downloaded, we pass in all sector roots
		// except for the partial one and pass in the data as well.
		sectorHashes := append(i.staticState.sectors.merkleRoots[:secIdx], i.staticState.sectors.merkleRoots[secIdx+1:]...)
		output.Proof = crypto.MerkleMixedRangeProof(sectorHashes, fullSec, int(modules.SectorSize), proofStart, proofEnd)
	}
	return output, types.ZeroCurrency
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
