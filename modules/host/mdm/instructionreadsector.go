package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// instructionReadSector is an instruction which reads from a sector specified
// by a merkle root.
type instructionReadSector struct {
	commonInstruction

	lengthOffset     uint64
	offsetOffset     uint64
	merkleRootOffset uint64
}

// staticDecodeReadSectorInstruction creates a new 'ReadSector' instruction from the
// provided generic instruction.
func (p *program) staticDecodeReadSectorInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadSector {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadSector, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadSectorLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIReadSectorLen, len(instruction.Args))
	}
	// Read args.
	rootOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	offsetOffset := binary.LittleEndian.Uint64(instruction.Args[8:16])
	lengthOffset := binary.LittleEndian.Uint64(instruction.Args[16:24])

	// Return instruction.
	return &instructionReadSector{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: instruction.Args[24] == 1,
			staticState:       p.staticProgramState,
		},
		lengthOffset:     lengthOffset,
		merkleRootOffset: rootOffset,
		offsetOffset:     offsetOffset,
	}, nil
}

// executeReadSector executes the 'ReadSector' instruction.
func executeReadSector(previousOutput output, ps *programState, length, offset uint64, sectorRoot crypto.Hash, merkleProof bool) output {
	// Validate the request.
	var err error
	switch {
	case offset+length > modules.SectorSize:
		err = fmt.Errorf("request is out of bounds %v + %v = %v > %v", offset, length, offset+length, modules.SectorSize)
	case length == 0:
		err = errors.New("length cannot be zero")
	case merkleProof && (offset%crypto.SegmentSize != 0 || length%crypto.SegmentSize != 0):
		err = fmt.Errorf("offset (%v) and length (%v) must be multiples of SegmentSize (%v) when requesting a Merkle proof", offset, length, crypto.SegmentSize)
	}
	if err != nil {
		return errOutput(err)
	}

	sectorData, err := ps.sectors.readSector(ps.host, sectorRoot)
	if err != nil {
		return errOutput(err)
	}
	readData := sectorData[offset : offset+length]

	// Construct the Merkle proof, if requested.
	var proof []crypto.Hash
	if merkleProof {
		proofStart := int(offset) / crypto.SegmentSize
		proofEnd := int(offset+length) / crypto.SegmentSize
		proof = crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	}

	// Return the output.
	return output{
		NewSize:       previousOutput.NewSize,       // size stays the same
		NewMerkleRoot: previousOutput.NewMerkleRoot, // root stays the same
		Output:        readData,
		Proof:         proof,
	}
}

// Execute executes the 'ReadSector' instruction.
func (i *instructionReadSector) Execute(previousOutput output) output {
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return errOutput(err)
	}
	offset, err := i.staticData.Uint64(i.offsetOffset)
	if err != nil {
		return errOutput(err)
	}
	sectorRoot, err := i.staticData.Hash(i.merkleRootOffset)
	if err != nil {
		return errOutput(err)
	}
	return executeReadSector(previousOutput, i.staticState, length, offset, sectorRoot, i.staticMerkleProof)
}

// Collateral is zero for the ReadSector instruction.
func (i *instructionReadSector) Collateral() types.Currency {
	return modules.MDMReadCollateral()
}

// Cost returns the cost of a ReadSector instruction.
func (i *instructionReadSector) Cost() (executionCost, refund types.Currency, err error) {
	length, err := i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return
	}
	executionCost, refund = modules.MDMReadCost(i.staticState.priceTable, length)
	return
}

// Memory returns the memory allocated by the 'ReadSector' instruction beyond
// the lifetime of the instruction.
func (i *instructionReadSector) Memory() uint64 {
	return modules.MDMReadMemory()
}

// Time returns the execution time of a 'ReadSector' instruction.
func (i *instructionReadSector) Time() (uint64, error) {
	return modules.MDMTimeReadSector, nil
}
