package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// programBuilder is a helper used for constructing programs one instruction at
// a time.
type programBuilder struct {
	// Costs contains the current running costs in the builder.
	Costs Costs

	pt              modules.RPCPriceTable
	programData     ProgramData
	programDataLen  uint64
	dataOffset      uint64 // the data offset for the next instruction
	instructions    Instructions
	numInstructions uint64 // set when creating the builder
}

// newProgramBuilder creates a new programBuilder.
func newProgramBuilder(pt modules.RPCPriceTable, dataLen, numInstructions uint64) programBuilder {
	return programBuilder{
		Costs: InitialProgramCosts(pt, dataLen, numInstructions),

		pt:              pt,
		programData:     make(ProgramData, dataLen),
		programDataLen:  dataLen,
		dataOffset:      0,
		instructions:    make(Instructions, 0, numInstructions),
		numInstructions: numInstructions,
	}
}

// AddAppendInstruction adds an append instruction to the builder, updating its
// internal state.
func (b *programBuilder) AddAppendInstruction(data ProgramData, merkleProof bool) error {
	if uint64(len(data)) != modules.SectorSize {
		return fmt.Errorf("expected length of data to be the size of a sector %v, was %v", modules.SectorSize, len(data))
	}
	offsetIncrement := modules.SectorSize
	if b.dataOffset+offsetIncrement > b.programDataLen {
		return fmt.Errorf("cannot add append instruction, the length of the program data would surpass the provided length %v", b.programDataLen)
	}

	i := NewAppendInstruction(b.dataOffset, merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	copy(b.programData[b.dataOffset:b.dataOffset+modules.SectorSize], data)
	b.dataOffset += offsetIncrement
	// Get costs for an append instruction.
	executionCost, refund := modules.MDMAppendCost(b.pt)
	costs := Costs{executionCost, refund, modules.MDMAppendCollateral(b.pt), modules.MDMAppendMemory(), modules.MDMTimeAppend}
	// Update costs.
	b.UpdateCosts(costs)

	return nil
}

// AddDropSectorsInstruction adds a dropsectors instruction to the builder,
// updating its internal state.
func (b *programBuilder) AddDropSectorsInstruction(numSectors uint64, merkleProof bool) error {
	offsetIncrement := uint64(8)
	if b.dataOffset+offsetIncrement > b.programDataLen {
		return fmt.Errorf("cannot add dropsectors instruction, the length of the program data would surpass the provided length %v", b.programDataLen)
	}

	i := NewDropSectorsInstruction(b.dataOffset, merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.LittleEndian.PutUint64(b.programData[b.dataOffset:b.dataOffset+8], numSectors)
	b.dataOffset += offsetIncrement
	// Get costs for a dropsectors instruction.
	executionCost, refund := modules.MDMDropSectorsCost(b.pt, numSectors)
	costs := Costs{executionCost, refund, modules.MDMDropSectorsCollateral(), modules.MDMDropSectorsMemory(), modules.MDMDropSectorsTime(numSectors)}
	// Update costs.
	b.UpdateCosts(costs)

	return nil
}

// AddHasSectorInstruction adds a hassector instruction to the builder, updating
// its internal state.
func (b *programBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) error {
	offsetIncrement := uint64(crypto.HashSize)
	if b.dataOffset+offsetIncrement > b.programDataLen {
		return fmt.Errorf("cannot add hassector instruction, the length of the program data would surpass the provided length %v", b.programDataLen)
	}

	i := NewHasSectorInstruction(b.dataOffset)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	copy(b.programData[b.dataOffset:b.dataOffset+crypto.HashSize], merkleRoot[:])
	b.dataOffset += offsetIncrement
	// Get costs for a hassector instruction.
	executionCost, refund := modules.MDMHasSectorCost(b.pt)
	costs := Costs{executionCost, refund, modules.MDMHasSectorCollateral(), modules.MDMHasSectorMemory(), modules.MDMTimeHasSector}
	// Update costs.
	b.UpdateCosts(costs)

	return nil
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// updating its internal state.
func (b *programBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) error {
	offsetIncrement := uint64(2*8 + crypto.HashSize)
	if b.dataOffset+offsetIncrement > b.programDataLen {
		return fmt.Errorf("cannot add readsector instruction, the length of the program data would surpass the provided length %v", b.programDataLen)
	}

	i := NewReadSectorInstruction(b.dataOffset, b.dataOffset+8, b.dataOffset+16, merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.LittleEndian.PutUint64(b.programData[b.dataOffset:b.dataOffset+8], length)
	binary.LittleEndian.PutUint64(b.programData[b.dataOffset+8:b.dataOffset+16], offset)
	copy(b.programData[b.dataOffset+16:b.dataOffset+16+crypto.HashSize], merkleRoot[:])
	b.dataOffset += offsetIncrement
	// Get costs for a readsector instruction.
	executionCost, refund := modules.MDMReadCost(b.pt, length)
	costs := Costs{executionCost, refund, modules.MDMReadCollateral(), modules.MDMReadMemory(), modules.MDMTimeReadSector}
	// Update costs.
	b.UpdateCosts(costs)

	return nil
}

// UpdateCosts updates the running costs for the program.
func (b *programBuilder) UpdateCosts(newCosts Costs) {
	b.Costs = b.Costs.Update(b.pt, newCosts)
}

// Finish finishes building the program and returns the final state including
// the instruction list, program data and final costs.
func (b *programBuilder) Finish() (Instructions, ProgramData, Costs, error) {
	// The number of instructions specified should equal the number added.
	if uint64(len(b.instructions)) != b.numInstructions {
		return nil, nil, Costs{}, fmt.Errorf("expected %v instructions, received %v", b.numInstructions, len(b.instructions))
	}
	// The data length specified should equal the amount added.
	if b.dataOffset != b.programDataLen {
		return nil, nil, Costs{}, fmt.Errorf("expected %v bytes of data, received %v", b.programDataLen, b.dataOffset)
	}

	b.Costs = b.Costs.FinalizeProgramCosts(b.pt)

	return b.instructions, b.programData, b.Costs, nil
}
