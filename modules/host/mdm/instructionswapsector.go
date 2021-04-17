package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/encoding"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionSwapSector is an instruction that swaps two sectors of a file
// contract.
type instructionSwapSector struct {
	commonInstruction

	sector1Offset uint64
	sector2Offset uint64
}

// staticDecodeSwapSectorInstruction creates a new 'SwapSector' instruction from the
// provided generic instruction.
func (p *program) staticDecodeSwapSectorInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierSwapSector {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierSwapSector, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCISwapSectorLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCISwapSectorLen, len(instruction.Args))
	}
	// Read args.
	sector1Offset := binary.LittleEndian.Uint64(instruction.Args[:8])
	sector2Offset := binary.LittleEndian.Uint64(instruction.Args[8:16])
	return &instructionSwapSector{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: instruction.Args[16] == 1,
			staticState:       p.staticProgramState,
		},
		sector1Offset: sector1Offset,
		sector2Offset: sector2Offset,
	}, nil
}

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionSwapSector) Batch() bool {
	return false
}

// Execute executes the 'SwapSector' instruction.
func (i *instructionSwapSector) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the data.
	offset1, err := i.staticData.Uint64(i.sector1Offset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	offset2, err := i.staticData.Uint64(i.sector2Offset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// Order the offsets so we don't need to do that later.
	if offset2 < offset1 {
		offset1, offset2 = offset2, offset1
	}

	ps := i.staticState
	newMerkleRoot, err := ps.sectors.swapSectors(offset1, offset2)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// If no proof was requested we are done.
	if !i.staticMerkleProof {
		return output{
			NewSize:       prevOutput.NewSize,
			NewMerkleRoot: newMerkleRoot,
		}, types.ZeroCurrency
	}

	// Get the swapped sectors. Since they have been swapped, the indices are
	// reversed.
	newRoots := i.staticState.sectors.merkleRoots
	oldSector1 := newRoots[offset2]
	oldSector2 := newRoots[offset1]

	// Create the first range and remember the original leaf hash for that range
	// since the leaves of the modified sectors are needed to verify the proof.
	var ranges []crypto.ProofRange
	var oldLeafHashes []crypto.Hash
	ranges = append(ranges, crypto.ProofRange{
		Start: offset1,
		End:   offset1 + 1,
	})
	oldLeafHashes = append(oldLeafHashes, oldSector1)

	// We only need the second range if the offsets aren't equal. Also remember
	// the leaf hash for that range.
	if offset1 != offset2 {
		ranges = append(ranges, crypto.ProofRange{
			Start: offset2,
			End:   offset2 + 1,
		})
		oldLeafHashes = append(oldLeafHashes, oldSector2)
	}

	// Create the proof and return the old leaf hashes of the modified sectors
	// as the data since the data is unused anyway. The renter needs the
	// original sector hashes to verify the proof against the old contract
	// merkle root and will then swap them and verify against the new root.
	proof := crypto.MerkleDiffProof(ranges, uint64(len(newRoots)), nil, ps.sectors.merkleRoots)
	data := encoding.Marshal(oldLeafHashes)

	return output{
		NewSize:       prevOutput.NewSize,
		NewMerkleRoot: newMerkleRoot,
		Output:        data,
		Proof:         proof,
	}, types.ZeroCurrency
}

// Collateral returns the collateral cost of adding one full sector.
func (i *instructionSwapSector) Collateral() types.Currency {
	return modules.MDMSwapSectorCollateral()
}

// Cost returns the Cost of this `SwapSector` instruction.
func (i *instructionSwapSector) Cost() (executionCost, storage types.Currency, err error) {
	executionCost = modules.MDMSwapSectorCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by the 'SwapSector' instruction beyond the
// lifetime of the instruction.
func (i *instructionSwapSector) Memory() uint64 {
	return modules.MDMSwapSectorMemory()
}

// Time returns the execution time of an 'SwapSector' instruction.
func (i *instructionSwapSector) Time() (uint64, error) {
	return modules.MDMTimeSwapSector, nil
}
