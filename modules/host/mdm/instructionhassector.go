package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionHasSector is an instruction which returns whether the host stores
// the sector with the given root or not.
type instructionHasSector struct {
	commonInstruction

	merkleRootOffset uint64
}

// staticDecodeHasSectorInstruction creates a new 'HasSector' instruction from
// the provided generic instruction.
func (p *program) staticDecodeHasSectorInstruction(instruction modules.Instruction) (instruction, error) {
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

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionHasSector) Batch() bool {
	return true
}

// Collateral is zero for the HasSector instruction.
func (i *instructionHasSector) Collateral() types.Currency {
	return modules.MDMHasSectorCollateral()
}

// Cost returns the cost of executing this instruction.
func (i *instructionHasSector) Cost() (executionCost, _ types.Currency, err error) {
	executionCost = modules.MDMHasSectorCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by this instruction beyond the end of its
// lifetime.
func (i *instructionHasSector) Memory() uint64 {
	return modules.MDMHasSectorMemory()
}

// Execute executes the 'HasSector' instruction.
func (i *instructionHasSector) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the operands.
	sectorRoot, err := i.staticData.Hash(i.merkleRootOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// Fetch the requested information.
	hasSector := i.staticState.host.HasSector(sectorRoot)

	// Return the output.
	out := []byte{0}
	if hasSector {
		out[0] = 1
	}

	return output{
		NewSize:       prevOutput.NewSize,       // size stays the same
		NewMerkleRoot: prevOutput.NewMerkleRoot, // root stays the same
		Output:        out,
	}, types.ZeroCurrency
}

// Time returns the execution time of an 'HasSector' instruction.
func (i *instructionHasSector) Time() (uint64, error) {
	return modules.MDMTimeHasSector, nil
}
