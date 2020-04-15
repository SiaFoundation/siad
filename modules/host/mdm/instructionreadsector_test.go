package mdm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newReadSectorInstruction is a convenience method for creating a single
// 'ReadSector' instruction.
func newReadSectorInstruction(length uint64, merkleProof bool, dataOffset uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := NewReadSectorInstruction(dataOffset, dataOffset+8, dataOffset+16, merkleProof)
	cost, refund := modules.MDMReadCost(pt, length)
	collateral := modules.MDMReadCollateral()
	return i, cost, refund, collateral, modules.MDMReadMemory(), modules.MDMTimeReadSector
}

// newReadSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// ReadSectorInstruction.
func newReadSectorProgram(length, offset uint64, merkleRoot crypto.Hash, pt modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64) {
	data := make([]byte, 8+8+crypto.HashSize)
	binary.LittleEndian.PutUint64(data[:8], length)
	binary.LittleEndian.PutUint64(data[8:16], offset)
	copy(data[16:], merkleRoot[:])
	initCost := modules.MDMInitCost(pt, uint64(len(data)), 1)
	i, cost, refund, collateral, memory, time := newReadSectorInstruction(length, true, 0, pt)
	cost, refund, collateral, memory = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), cost, refund, collateral, memory, time)
	instructions := []modules.Instruction{i}
	cost = cost.Add(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit))
	return instructions, data, cost, refund, collateral, memory
}

// TestInstructionReadSector tests executing a program with a single
// ReadSectorInstruction.
func TestInstructionReadSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to read a full sector from the host.
	pt := newTestPriceTable()
	readLen := modules.SectorSize
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(10)
	root := so.sectorRoots[0]
	instructions, programData, cost, refund, collateral, memory := newReadSectorProgram(readLen, 0, root, pt)
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Expected outputs.
	outputData, err := host.ReadSector(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedOutputs := []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        outputData,
			},
			cost.Sub(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit)),
			collateral,
			refund,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, r)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	lastOutput, err := testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}
	sectorData := lastOutput.Output

	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}

	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset
	instructions, programData, cost, refund, collateral, memory = newReadSectorProgram(length, offset, so.sectorRoots[0], pt)
	r = bytes.NewReader(programData)
	dataLen = uint64(len(programData))

	// Expected outputs.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	outputData = sectorData[modules.SectorSize/2:]
	expectedOutputs = []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         proof,
				Output:        outputData,
			},
			cost.Sub(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit)),
			collateral,
			refund,
		},
	}

	// Execute it.
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, r)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// No need to finalize the program since an this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
