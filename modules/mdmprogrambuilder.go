package modules

import (
	"bytes"
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// ProgramBuilder is a helper used for constructing programs one instruction at
// a time.
type ProgramBuilder struct {
	dataBuf      *bytes.Buffer
	dataOffset   uint64 // the data offset for the next instruction
	instructions []Instruction
}

// NewProgramBuilder creates a new ProgramBuilder.
func NewProgramBuilder() ProgramBuilder {
	return ProgramBuilder{
		dataBuf:      new(bytes.Buffer),
		dataOffset:   0,
		instructions: make([]Instruction, 0),
	}
}

// AddAppendInstruction adds an append instruction to the builder, updating its
// internal state.
func (b *ProgramBuilder) AddAppendInstruction(data ProgramData, merkleProof bool) error {
	if uint64(len(data)) != SectorSize {
		return fmt.Errorf("expected length of data to be the size of a sector %v, was %v", SectorSize, len(data))
	}

	i := NewAppendInstruction(uint64(b.dataBuf.Len()), merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, data)

	return nil
}

// AddDropSectorsInstruction adds a dropsectors instruction to the builder,
// updating its internal state.
func (b *ProgramBuilder) AddDropSectorsInstruction(numSectors uint64, merkleProof bool) {
	i := NewDropSectorsInstruction(uint64(b.dataBuf.Len()), merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, numSectors)
}

// AddHasSectorInstruction adds a hassector instruction to the builder, updating
// its internal state.
func (b *ProgramBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) {
	i := NewHasSectorInstruction(uint64(b.dataBuf.Len()))
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, merkleRoot[:])
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// updating its internal state.
func (b *ProgramBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	dataOffset := uint64(b.dataBuf.Len())
	i := NewReadSectorInstruction(dataOffset, dataOffset+8, dataOffset+16, merkleProof)
	b.instructions = append(b.instructions, i)
	// Update the program data.
	binary.Write(b.dataBuf, binary.LittleEndian, length)
	binary.Write(b.dataBuf, binary.LittleEndian, offset)
	binary.Write(b.dataBuf, binary.LittleEndian, merkleRoot[:])
}

// Program finishes building the program and returns it.
func (b *ProgramBuilder) Program() Program {
	programData := b.dataBuf.Bytes()
	reader := bytes.NewReader(programData)

	program := Program{
		Instructions: b.instructions,
		Data:         reader,
		DataLen:      uint64(len(programData)),
	}
	return program
}

// Values returns a list of all running values including values upon program
// initialization as well as after each instruction, as well as the final set of
// program values.
func (b *ProgramBuilder) Values(pt *RPCPriceTable, finalized bool) ([]RunningProgramValues, ProgramValues, error) {
	programData := b.dataBuf.Bytes()

	// Store intermediate values for every instruction plus the initial program
	// state.
	allRunningValues := make([]RunningProgramValues, 0, len(b.instructions)+1)

	// Get the initial program values.
	runningValues := InitialProgramValues(pt, uint64(len(programData)), uint64(len(b.instructions)))
	allRunningValues = append(allRunningValues, runningValues)

	// Iterate over instructions, adding their costs and saving running costs.
	for _, i := range b.instructions {
		var values InstructionValues
		switch i.Specifier {
		case SpecifierAppend:
			executionCost, refund := MDMAppendCost(pt)
			values = InstructionValues{executionCost, refund, MDMAppendCollateral(pt), MDMAppendMemory(), MDMTimeAppend, false}
		case SpecifierDropSectors:
			numSectorsOffset := binary.LittleEndian.Uint64(i.Args[:8])
			numSectors := binary.LittleEndian.Uint64(programData[numSectorsOffset : numSectorsOffset+8])
			executionCost, refund := MDMDropSectorsCost(pt, numSectors)
			values = InstructionValues{executionCost, refund, MDMDropSectorsCollateral(), MDMDropSectorsMemory(), MDMDropSectorsTime(numSectors), false}
		case SpecifierHasSector:
			executionCost, refund := MDMHasSectorCost(pt)
			values = InstructionValues{executionCost, refund, MDMHasSectorCollateral(), MDMHasSectorMemory(), MDMTimeHasSector, true}
		case SpecifierReadSector:
			lengthOffset := binary.LittleEndian.Uint64(i.Args[16:24])
			length := binary.LittleEndian.Uint64(programData[lengthOffset : lengthOffset+8])
			executionCost, refund := MDMReadCost(pt, length)
			values = InstructionValues{executionCost, refund, MDMReadCollateral(), MDMReadMemory(), MDMTimeReadSector, true}
		default:
			return nil, ProgramValues{}, fmt.Errorf("unknown instruction specifier: %v", i.Specifier)
		}
		runningValues.AddValues(pt, values)
		allRunningValues = append(allRunningValues, runningValues)
	}

	// Add the cost of finalizing the program.
	finalValues := runningValues.FinalizeProgramValues(pt, finalized)

	return allRunningValues, finalValues, nil
}

// NewAppendInstruction creates an Instruction from arguments.
func NewAppendInstruction(dataOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierAppend,
		Args:      make([]byte, RPCIAppendLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], dataOffset)
	if merkleProof {
		i.Args[8] = 1
	}
	return i
}

// NewDropSectorsInstruction creates an Instruction from arguments.
func NewDropSectorsInstruction(numSectorsOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierDropSectors,
		Args:      make([]byte, RPCIDropSectorsLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], numSectorsOffset)
	if merkleProof {
		i.Args[8] = 1
	}
	return i
}

// NewHasSectorInstruction creates an Instruction from arguments.
func NewHasSectorInstruction(merkleRootOffset uint64) Instruction {
	i := Instruction{
		Specifier: SpecifierHasSector,
		Args:      make([]byte, RPCIHasSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	return i
}

// NewReadSectorInstruction creates an Instruction from arguments.
func NewReadSectorInstruction(lengthOffset, offsetOffset, merkleRootOffset uint64, merkleProof bool) Instruction {
	i := Instruction{
		Specifier: SpecifierReadSector,
		Args:      make([]byte, RPCIReadSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	binary.LittleEndian.PutUint64(i.Args[8:16], offsetOffset)
	binary.LittleEndian.PutUint64(i.Args[16:24], lengthOffset)
	if merkleProof {
		i.Args[24] = 1
	}
	return i
}
