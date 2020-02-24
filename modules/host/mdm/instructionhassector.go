package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instructionHasSector is an instruction which returns whether the host stores
// the sector with the given root or not.
type instructionHasSector struct {
	commonInstruction

	merkleRootOffset uint64
}

// NewHasSectorInstruction creates a modules.Instruction from arguments.
func NewHasSectorInstruction(merkleRootOffset uint64) modules.Instruction {
	i := modules.Instruction{
		Specifier: modules.SpecifierHasSector,
		Args:      make([]byte, modules.RPCIHasSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	return i
}

// staticDecodeHasSectorInstruction creates a new 'HasSector' instruction from
// the provided generic instruction.
func (p *Program) staticDecodeHasSectorInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierHasSector {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierHasSector, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIHasSectorLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIHasSectorLen, len(instruction.Args))
	}
	// Read args.
	rootOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	return &instructionHasSector{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: false,
			staticState:       p.staticProgramState,
		},
		merkleRootOffset: rootOffset,
	}, nil
}

// Cost returns the cost of executing this instruction.
func (i *instructionHasSector) Cost() (types.Currency, error) {
	return HasSectorCost(i.staticState.priceTable), nil
}

// Execute executes the 'HasSector' instruction.
func (i *instructionHasSector) Execute(prevOutput Output) Output {
	// Fetch the operands.
	sectorRoot, err := i.staticData.Hash(i.merkleRootOffset)
	if err != nil {
		return outputFromError(err)
	}
	// Fetch the requested information
	hasSector, err := i.staticState.host.HasSector(sectorRoot)
	if err != nil {
		return outputFromError(err)
	}
	// Return the output.
	output := []byte{0}
	if hasSector {
		output[0] = 1
	}
	return Output{
		NewSize:       prevOutput.NewSize,       // size stays the same
		NewMerkleRoot: prevOutput.NewMerkleRoot, // root stays the same
		Output:        output,
	}
}

// ReadOnly for the 'HasSector' instruction is 'true'.
func (i *instructionHasSector) ReadOnly() bool {
	return true
}
