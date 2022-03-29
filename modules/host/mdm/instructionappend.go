package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionAppend is an instruction that appends a full sector to a
// filecontract.
type instructionAppend struct {
	commonInstruction

	dataOffset uint64
}

// staticDecodeAppendInstruction creates a new 'Append' instruction from the
// provided generic instruction.
func (p *program) staticDecodeAppendInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierAppend {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierAppend, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIAppendLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIAppendLen, len(instruction.Args))
	}
	// Read args.
	dataOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	return &instructionAppend{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: instruction.Args[8] == 1,
			staticState:       p.staticProgramState,
		},
		dataOffset: dataOffset,
	}, nil
}

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionAppend) Batch() bool {
	return false
}

// Execute executes the 'Append' instruction.
func (i *instructionAppend) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the data.
	sectorData, err := i.staticData.Bytes(i.dataOffset, modules.SectorSize)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	newFileSize := prevOutput.NewSize + modules.SectorSize

	// TODO: How to update finances with EA?
	// i.staticState.potentialStorageRevenue = i.staticState.potentialStorageRevenue.Add(types.ZeroCurrency)
	// i.staticState.riskedCollateral = i.staticState.riskedCollateral.Add(types.ZeroCurrency)
	// i.staticState.potentialUploadRevenue = i.staticState.potentialUploadRevenue.Add(types.ZeroCurrency)

	ps := i.staticState
	oldSectors := ps.sectors.merkleRoots
	newMerkleRoot, err := ps.sectors.appendSector(sectorData)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// Construct proof if necessary.
	var proof []crypto.Hash
	if i.staticMerkleProof {
		proof = crypto.MerkleDiffProof(nil, uint64(len(oldSectors)), nil, oldSectors)
	}

	return output{
		NewSize:       newFileSize,
		NewMerkleRoot: newMerkleRoot,
		Proof:         proof,
	}, types.ZeroCurrency
}

// Collateral returns the collateral cost of adding one full sector.
func (i *instructionAppend) Collateral() types.Currency {
	duration := i.staticState.staticRemainingDuration
	return modules.MDMAppendCollateral(i.staticState.priceTable, duration)
}

// Cost returns the Cost of this `Append` instruction.
func (i *instructionAppend) Cost() (executionCost, storage types.Currency, err error) {
	duration := i.staticState.staticRemainingDuration
	executionCost, storage = modules.MDMAppendCost(i.staticState.priceTable, duration)
	return
}

// Memory returns the memory allocated by the 'Append' instruction beyond the
// lifetime of the instruction.
func (i *instructionAppend) Memory() uint64 {
	return modules.MDMAppendMemory()
}

// Time returns the execution time of an 'Append' instruction.
func (i *instructionAppend) Time() (uint64, error) {
	return modules.MDMTimeAppend, nil
}
