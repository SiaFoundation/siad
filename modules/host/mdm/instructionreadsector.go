package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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

// NewReadSectorInstruction creates a modules.Instruction from arguments.
func NewReadSectorInstruction(lengthOffset, offsetOffset, merkleRootOffset uint64, merkleProof bool) modules.Instruction {
	rsi := modules.Instruction{
		Specifier: modules.SpecifierReadSector,
		Args:      make([]byte, modules.RPCIReadSectorLen),
	}
	binary.LittleEndian.PutUint64(rsi.Args[:8], merkleRootOffset)
	binary.LittleEndian.PutUint64(rsi.Args[8:16], offsetOffset)
	binary.LittleEndian.PutUint64(rsi.Args[16:24], lengthOffset)
	if merkleProof {
		rsi.Args[24] = 1
	}
	return rsi
}

// staticDecodeReadSectorInstruction creates a new 'ReadSector' instruction from the
// provided generic instruction.
func (p *Program) staticDecodeReadSectorInstruction(instruction modules.Instruction) (instruction, error) {
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
	return &instructionReadSector{
		commonInstruction: commonInstruction{
			staticContractSize: p.finalContractSize,
			staticData:         p.staticData,
			staticMerkleProof:  instruction.Args[24] == 1,
			staticState:        p.staticProgramState,
		},
		lengthOffset:     lengthOffset,
		merkleRootOffset: rootOffset,
		offsetOffset:     offsetOffset,
	}, nil
}

// Cost returns the cost of executing this instruction.
func (i *instructionReadSector) Cost() Cost {
	return ReadSectorCost()
}

// Execute executes the 'Read' instruction.
func (i *instructionReadSector) Execute(fcRoot crypto.Hash) Output {
	// Subtract cost from budget beforehand.
	var err error
	i.staticState.remainingBudget, err = i.staticState.remainingBudget.Sub(ReadSectorCost())
	if err != nil {
		return outputFromError(err)
	}
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOffset)
	if err != nil {
		return outputFromError(err)
	}
	offset, err := i.staticData.Uint64(i.offsetOffset)
	if err != nil {
		return outputFromError(err)
	}
	sectorRoot, err := i.staticData.Hash(i.merkleRootOffset)
	if err != nil {
		return outputFromError(err)
	}
	// Validate the request.
	switch {
	case offset+length > modules.SectorSize:
		err = fmt.Errorf("request is out of bounds %v + %v = %v > %v", offset, length, offset+length, modules.SectorSize)
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
		NewSize:       i.staticContractSize, // size stays the same
		NewMerkleRoot: fcRoot,               // root stays the same
		Output:        data,
		Proof:         proof,
	}
}

// ReadOnly for the 'ReadSector' instruction is 'true'.
func (i *instructionReadSector) ReadOnly() bool {
	return true
}
