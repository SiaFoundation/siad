package mdm

import (
	"encoding/binary"
	"errors"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instructionDropSectors is an instruction that drops the given number of
// sectors from the contract.
type instructionDropSectors struct {
	commonInstruction

	numSectorsOffset uint64
}

// NewDropSectorsInstruction creates a modules.Instruction from arguments.
func NewDropSectorsInstruction(numSectorsOffset uint64, merkleProof bool) modules.Instruction {
	i := modules.Instruction{
		Specifier: modules.SpecifierDropSectors,
		Args:      make([]byte, modules.RPCIDropSectorsLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], numSectorsOffset)
	if merkleProof {
		i.Args[8] = 1
	}
	return i
}

// staticDecodeDropSectorsInstruction creates a new 'DropSectors' instruction from the
// provided generic instruction.
func (p *Program) staticDecodeDropSectorsInstruction(instruction modules.Instruction) (instruction, error) {
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

// Execute executes the 'DropSectors' instruction.
//
// 1. The data is fetched and checked for validity:
//
//   a. The number of sectors must be less than or equal to the number of
//   sectors in the contract.
//
//   b. If the number of sectors is 0 this instruction is a noop.
//
// 2. Remove the specified number of merkle roots.
//
// 3. Add the merkle roots that were just removed to the list of removed
// sectors.
//
// 4. Compute the new merkle root of the contract.
//
// TODO: finances + proof
func (i *instructionDropSectors) Execute(prevOutput output) output {
	// Fetch the data.
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return errOutput(errors.New("bad input: numSectorsOffset"))
	}

	oldSize := prevOutput.NewSize
	oldNumSectors := oldSize / modules.SectorSize

	// Verify input.
	if numSectorsDropped > oldNumSectors {
		return errOutput(fmt.Errorf("bad input: numSectors (%v) is greater than the number of sectors in the contract (%v)", numSectorsDropped, oldNumSectors))
	}

	newNumSectors := oldNumSectors - numSectorsDropped
	newSize := newNumSectors * modules.SectorSize

	ps := i.staticState

	// Construct the proof, if necessary, before updating the roots.
	var proof []crypto.Hash
	if i.staticMerkleProof && numSectorsDropped > 0 {
		// First dropped sector.
		start := int(newNumSectors)
		end := start + 1
		proof = crypto.MerkleSectorRangeProof(ps.sectors.merkleRoots, start, end)
	}

	newMerkleRoot := ps.sectors.dropSectors(numSectorsDropped)

	// TODO: Update finances.

	return output{
		NewSize:       newSize,
		NewMerkleRoot: newMerkleRoot,
		Proof:         proof,
	}
}

// Cost returns the Cost of the DropSectors instruction.
func (i *instructionDropSectors) Cost() (types.Currency, types.Currency, error) {
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return types.Currency{}, types.Currency{}, errors.New("bad input: numSectorsOffset")
	}
	cost, refund := MDMDropSectorsCost(i.staticState.priceTable, numSectorsDropped)
	return cost, refund, nil
}

// ReadOnly for the 'DropSectors' instruction is 'false'.
func (i *instructionDropSectors) ReadOnly() bool {
	return false
}
