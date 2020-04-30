package mdm

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// programBuilder is a helper used for constructing programs one instruction at
// a time.
type programBuilder struct {
	dataBuf      *bytes.Buffer
	dataOffset   uint64 // the data offset for the next instruction
	instructions []modules.Instruction
}

// newProgramBuilder creates a new programBuilder.
func newProgramBuilder() programBuilder {
	return programBuilder{
		dataBuf:      new(bytes.Buffer),
		dataOffset:   0,
		instructions: make([]modules.Instruction, 0),
	}
}

// AddAppendInstruction adds an append instruction to the builder, updating its
// internal state.
func (b *programBuilder) AddAppendInstruction(data modules.ProgramData, merkleProof bool) error {
	if uint64(len(data)) != modules.SectorSize {
		return fmt.Errorf("expected length of data to be the size of a sector %v, was %v", modules.SectorSize, len(data))
	}

	i := NewAppendInstruction(uint64(b.dataBuf.Len()), merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, data)

	return nil
}

// AddDropSectorsInstruction adds a dropsectors instruction to the builder,
// updating its internal state.
func (b *programBuilder) AddDropSectorsInstruction(numSectors uint64, merkleProof bool) {
	i := NewDropSectorsInstruction(uint64(b.dataBuf.Len()), merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, numSectors)
}

// AddHasSectorInstruction adds a hassector instruction to the builder, updating
// its internal state.
func (b *programBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) {
	i := NewHasSectorInstruction(uint64(b.dataBuf.Len()))
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, merkleRoot[:])
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// updating its internal state.
func (b *programBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	dataOffset := uint64(b.dataBuf.Len())
	i := NewReadSectorInstruction(dataOffset, dataOffset+8, dataOffset+16, merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, length)
	binary.Write(b.dataBuf, binary.LittleEndian, offset)
	binary.Write(b.dataBuf, binary.LittleEndian, merkleRoot[:])
}

// Finalize finishes building the program and returns the final state including
// the instruction list, values for every step, final values and program data.
func (b *programBuilder) Finalize(pt *modules.RPCPriceTable) (modules.Program, []RunningProgramValues, ProgramValues, error) {
	programData := b.dataBuf.Bytes()
	dataLen := uint64(len(programData))

	// Store intermediate values for every instruction plus the initial program
	// state.
	allRunningValues := make([]RunningProgramValues, 0, len(b.instructions)+1)

	// Get the initial program values.
	runningValues := initialProgramValues(pt, dataLen, uint64(len(b.instructions)))
	allRunningValues = append(allRunningValues, runningValues)

	// Iterate over instructions, adding their costs and saving running costs.
	for _, i := range b.instructions {
		var values InstructionValues
		switch i.Specifier {
		case modules.SpecifierAppend:
			executionCost, refund := modules.MDMAppendCost(pt)
			values = InstructionValues{executionCost, refund, modules.MDMAppendCollateral(pt), modules.MDMAppendMemory(), modules.MDMTimeAppend, false}
		case modules.SpecifierDropSectors:
			numSectorsOffset := binary.LittleEndian.Uint64(i.Args[:8])
			numSectors := binary.LittleEndian.Uint64(programData[numSectorsOffset : numSectorsOffset+8])
			executionCost, refund := modules.MDMDropSectorsCost(pt, numSectors)
			values = InstructionValues{executionCost, refund, modules.MDMDropSectorsCollateral(), modules.MDMDropSectorsMemory(), modules.MDMDropSectorsTime(numSectors), false}
		case modules.SpecifierHasSector:
			executionCost, refund := modules.MDMHasSectorCost(pt)
			values = InstructionValues{executionCost, refund, modules.MDMHasSectorCollateral(), modules.MDMHasSectorMemory(), modules.MDMTimeHasSector, true}
		case modules.SpecifierReadSector:
			lengthOffset := binary.LittleEndian.Uint64(i.Args[16:24])
			length := binary.LittleEndian.Uint64(programData[lengthOffset : lengthOffset+8])
			executionCost, refund := modules.MDMReadCost(pt, length)
			values = InstructionValues{executionCost, refund, modules.MDMReadCollateral(), modules.MDMReadMemory(), modules.MDMTimeReadSector, true}
		default:
			return modules.Program{}, nil, ProgramValues{}, fmt.Errorf("unknown instruction specifier: %v", i.Specifier)
		}
		runningValues.addValues(pt, values)
		allRunningValues = append(allRunningValues, runningValues)
	}

	// Finalize the program costs.
	finalValues := runningValues.finalizeProgramValues(pt)

	program := modules.Program{
		Instructions: b.instructions,
		Data:         bytes.NewReader(programData),
		DataLen:      dataLen,
	}
	return program, allRunningValues, finalValues, nil
}
