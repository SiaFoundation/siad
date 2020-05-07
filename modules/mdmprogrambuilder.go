package modules

import (
	"encoding/binary"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// Expected bandwidth constants
const (
	dlBandwidthReadSector = uint64(10220)
	ulBandwidthReadSector = uint64(18980)

	dlBandwidthHasSector = uint64(4380)
	ulBandwidthHasSector = uint64(10220)
)

type (
	// ProgramBuilder is a helper type to easily create programs and compute
	// their cost.
	ProgramBuilder struct {
		staticPT    *RPCPriceTable
		readonly    bool
		program     Program
		programData []byte

		// Cost related fields.
		executionCost    types.Currency
		potentialRefund  types.Currency
		riskedCollateral types.Currency
		usedMemory       uint64

		downloadBandwidth uint64
		uploadBandwidth   uint64
	}
)

// NewProgramBuilder creates an empty program builder.
func NewProgramBuilder(pt *RPCPriceTable) *ProgramBuilder {
	pb := &ProgramBuilder{
		readonly:   true, // every program starts readonly
		staticPT:   pt,
		usedMemory: MDMInitMemory(),
	}
	return pb
}

// AddHasSectorInstruction adds a HasSector instruction to the program.
func (pb *ProgramBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) {
	// Compute the argument offsets.
	merkleRootOffset := uint64(len(pb.programData))
	// Extend the programData.
	pb.programData = append(pb.programData, merkleRoot[:]...)
	// Create the instruction.
	i := NewHasSectorInstruction(merkleRootOffset)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMHasSectorCollateral()
	cost, refund := MDMHasSectorCost(pb.staticPT)
	memory := MDMHasSectorMemory()
	time := uint64(MDMTimeHasSector)
	pb.addInstruction(collateral, cost, refund, memory, time, dlBandwidthHasSector, ulBandwidthHasSector)
}

// AddReadSectorInstruction adds a ReadSector instruction to the program.
func (pb *ProgramBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	// Compute the argument offsets.
	lengthOffset := uint64(len(pb.programData))
	offsetOffset := uint64(lengthOffset + 8)
	merkleRootOffset := uint64(offsetOffset + 8)
	// Extend the programData.
	pb.programData = append(pb.programData, make([]byte, 8+8+crypto.HashSize)...)
	// Write the arguments to the program data.
	binary.LittleEndian.PutUint64(pb.programData[lengthOffset:offsetOffset], length)
	binary.LittleEndian.PutUint64(pb.programData[offsetOffset:merkleRootOffset], offset)
	copy(pb.programData[merkleRootOffset:], merkleRoot[:])
	// Create the instruction.
	i := NewReadSectorInstruction(lengthOffset, offsetOffset, merkleRootOffset, merkleProof)
	// Append instruction
	pb.program = append(pb.program, i)
	// Update cost, collateral and memory usage.
	collateral := MDMReadCollateral()
	cost, refund := MDMReadCost(pb.staticPT, length)
	memory := MDMReadMemory()
	time := uint64(MDMTimeReadSector)
	pb.addInstruction(collateral, cost, refund, memory, time, dlBandwidthReadSector, ulBandwidthReadSector)
}

// BandwidthCost returns the current bandwidth cost of the program being built
// by the builder. Note this is an overestimate of the actual costs to ensure
// there is ample budget.
func (pb *ProgramBuilder) BandwidthCost() types.Currency {
	return MDMBandwidthCost(pb.staticPT, pb.uploadBandwidth, pb.downloadBandwidth)
}

// Cost returns the current cost of the program being built by the builder. If
// 'finalized' is 'true', the memory cost of finalizing the program is included.
func (pb *ProgramBuilder) Cost(finalized bool) (cost, refund, collateral types.Currency) {
	// Calculate the init cost.
	cost = MDMInitCost(pb.staticPT, uint64(len(pb.programData)), uint64(len(pb.program)))

	// Add the cost of the added instructions
	cost = cost.Add(pb.executionCost)

	// Add the cost of finalizing the program.
	if !pb.readonly && finalized {
		cost = cost.Add(MDMMemoryCost(pb.staticPT, pb.usedMemory, MDMTimeCommit))
	}

	// Add the bandwidth cost
	cost = cost.Add(pb.BandwidthCost())

	return cost, pb.potentialRefund, pb.riskedCollateral
}

// Program returns the built program and programData.
func (pb *ProgramBuilder) Program() (Program, []byte) {
	return pb.program, pb.programData
}

// addInstruction adds the collateral, cost, refund and memory cost of an
// instruction to the builder's state.
func (pb *ProgramBuilder) addInstruction(collateral, cost, refund types.Currency, memory, time, downloadBandwidth, uploadBandwidth uint64) {
	// Update collateral
	pb.riskedCollateral = pb.riskedCollateral.Add(collateral)
	// Update memory and memory cost.
	pb.usedMemory += memory
	memoryCost := MDMMemoryCost(pb.staticPT, pb.usedMemory, time)
	pb.executionCost = pb.executionCost.Add(memoryCost)
	// Update execution cost and refund.
	pb.executionCost = pb.executionCost.Add(cost)
	pb.potentialRefund = pb.potentialRefund.Add(refund)
	// Update bandwidth
	pb.downloadBandwidth += downloadBandwidth
	pb.uploadBandwidth += uploadBandwidth
}

// NewHasSectorInstruction creates a modules.Instruction from arguments.
func NewHasSectorInstruction(merkleRootOffset uint64) Instruction {
	i := Instruction{
		Specifier: SpecifierHasSector,
		Args:      make([]byte, RPCIHasSectorLen),
	}
	binary.LittleEndian.PutUint64(i.Args[:8], merkleRootOffset)
	return i
}

// NewReadSectorInstruction creates a modules.Instruction from arguments.
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
