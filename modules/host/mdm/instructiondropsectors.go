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
func (i *instructionDropSectors) Execute(prevOutput Output) Output {
	// Fetch the data.
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return outputFromError(errors.New("bad input: numSectorsOffset"))
	}

	oldSize := prevOutput.NewSize
	oldNumSectors := oldSize / modules.SectorSize

	// Verify input.
	if numSectorsDropped > oldNumSectors {
		return outputFromError(fmt.Errorf("bad input: numSectors %v is greater than the number of sectors in the contract %v", numSectorsDropped, oldNumSectors))
	}

	newNumSectors := oldNumSectors - numSectorsDropped
	newSize := newNumSectors * modules.SectorSize

	ps := i.staticState

	// Update the roots.
	droppedRoots := ps.merkleRoots[newNumSectors:]
	ps.merkleRoots = ps.merkleRoots[:newNumSectors]

	// Update the storage obligation.
	ps.sectorsRemoved = append(ps.sectorsRemoved, droppedRoots...)

	// Compute the new merkle root of the contract.
	newMerkleRoot := cachedMerkleRoot(ps.merkleRoots)

	// TODO: Update finances.

	// TODO: Construct proof if necessary.
	var proof []crypto.Hash
	if i.staticMerkleProof {
		start := len(ps.merkleRoots)
		end := start + 1
		proof = crypto.MerkleSectorRangeProof(ps.merkleRoots, start, end)
	}

	return Output{
		NewSize:       newSize,
		NewMerkleRoot: newMerkleRoot,
		Proof:         proof,
	}
}

// Cost returns the Cost of the DropSectors instruction.
func (i *instructionDropSectors) Cost() (types.Currency, error) {
	numSectorsDropped, err := i.staticData.Uint64(i.numSectorsOffset)
	if err != nil {
		return types.Currency{}, errors.New("bad input: numSectorsOffset")
	}
	return MDMDropSectorsCost(i.staticState.priceTable, numSectorsDropped), nil
}

// ReadOnly for the 'DropSectors' instruction is 'false'.
func (i *instructionDropSectors) ReadOnly() bool {
	return false
}
