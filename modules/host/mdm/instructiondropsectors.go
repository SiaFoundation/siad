package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionDropSectors is an instruction that drops the given number of
// sectors from the contract.
type instructionDropSectors struct {
	commonInstruction

	numSectorsOffset uint64
}

// staticDecodeDropSectorsInstruction creates a new 'DropSectors' instruction from the
// provided generic instruction.
func (p *program) staticDecodeDropSectorsInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierDropSectors {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierDropSectors, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIDropSectorsLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIDropSectorsLen, len(instruction.Args))
	}
	// Read args.
	numSectorsOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	return &instructionDropSectors{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: instruction.Args[8] == 1,
			staticState:       p.staticProgramState,
		},
		numSectorsOffset: numSectorsOffset,
	}, nil
}

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionDropSectors) Batch() bool {
	return false
}

// Execute executes the 'DropSectors' instruction.
//
// If the number of sectors is 0 this instruction is a noop.
func (i *instructionDropSectors) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the data.
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return errOutput(fmt.Errorf("bad input: numSectorsOffset: %v", err)), types.ZeroCurrency
	}

	// Verify input.
	oldNumSectors := prevOutput.NewSize / modules.SectorSize
	err = dropSectorsVerify(numSectorsDropped, oldNumSectors)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	newNumSectors := oldNumSectors - numSectorsDropped
	ps := i.staticState

	// Construct the proof, if necessary, before updating the roots.
	//
	// If no sectors were dropped or all sectors were dropped, the proof should
	// be empty. In the latter case, we also send the leaf hashes of the dropped
	// leaves, which is enough to compute and verify the original merkle roof.
	var proof []crypto.Hash
	if i.staticMerkleProof && numSectorsDropped > 0 && newNumSectors > 0 {
		// Create proof with range covering the dropped sectors.
		proof = crypto.MerkleSectorRangeProof(ps.sectors.merkleRoots, int(newNumSectors), int(oldNumSectors))
	}

	newMerkleRoot, err := ps.sectors.dropSectors(numSectorsDropped)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// TODO: Update finances.

	return output{
		NewSize:       newNumSectors * modules.SectorSize,
		NewMerkleRoot: newMerkleRoot,
		Proof:         proof,
	}, types.ZeroCurrency
}

// dropSectorsVerify verifies the input to a DropSectors instruction.
func dropSectorsVerify(numSectorsDropped, oldNumSectors uint64) error {
	if numSectorsDropped > oldNumSectors {
		return fmt.Errorf("bad input: numSectors (%v) is greater than the number of sectors in the contract (%v)", numSectorsDropped, oldNumSectors)
	}
	return nil
}

// Collateral is zero for the DropSectors instruction.
func (i *instructionDropSectors) Collateral() types.Currency {
	return modules.MDMDropSectorsCollateral()
}

// Cost returns the Cost of the DropSectors instruction.
func (i *instructionDropSectors) Cost() (executionCost, _ types.Currency, err error) {
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		err = fmt.Errorf("bad input: numSectorsOffset: %v", err)
		return
	}
	executionCost = modules.MDMDropSectorsCost(i.staticState.priceTable, numSectorsDropped)
	return
}

// Memory returns the memory allocated by the 'DropSectors' instruction beyond
// the lifetime of the instruction.
func (i *instructionDropSectors) Memory() uint64 {
	return modules.MDMDropSectorsMemory()
}

// Time returns the execution time of the 'DropSectors' instruction.
func (i *instructionDropSectors) Time() (uint64, error) {
	numDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return 0, err
	}
	return modules.MDMDropSectorsTime(numDropped), nil
}
