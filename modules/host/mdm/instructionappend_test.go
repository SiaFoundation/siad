package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newAppendInstruction is a convenience method for creating a single
// 'Append' instruction.
func newAppendInstruction(merkleProof bool, dataOffset uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := NewAppendInstruction(dataOffset, merkleProof)
	cost, refund := modules.MDMAppendCost(pt)
	collateral := modules.MDMAppendCollateral(pt)
	return i, cost, refund, collateral, modules.MDMAppendMemory(), modules.MDMTimeAppend
}

// newAppendProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// AppendInstruction.
func newAppendProgram(sectorData []byte, merkleProof bool, pt modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	initCost := modules.MDMInitCost(pt, uint64(len(sectorData)), 1)
	i, cost, refund, collateral, memory, time := newAppendInstruction(merkleProof, 0, pt)
	cost, refund, collateral, memory, time = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), 0, cost, refund, collateral, memory, time)
	instructions := []modules.Instruction{i}
	cost = cost.Add(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit))
	return instructions, sectorData, cost, refund, collateral, memory, time
}

// TestInstructionAppend tests executing a program with a single
// AppendInstruction.
func TestInstructionAppend(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to append a full sector to a storage obligation.
	appendData1 := randomSectorData()
	appendDataRoot1 := crypto.MerkleRoot(appendData1)
	pt := newTestPriceTable()
	instructions, programData, cost, refund, collateral, memory, time := newAppendProgram(appendData1, true, pt)
	dataLen := uint64(len(programData))

	// Verify the costs.
	expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime, err := EstimateProgramCosts(pt, instructions, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	err = testCompareCosts(cost, refund, collateral, memory, time, expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: crypto.MerkleRoot(appendData1),
				Proof:         []crypto.Hash{},
			},
			cost.Sub(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit)),
			collateral,
			refund,
		},
	}

	// Execute it.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) > 0 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 0)
	}
	if len(so.sectorRoots) > 0 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 0)
	}
	// Finalize the program.
	if err := finalize(so); err != nil {
		t.Fatal(err)
	}
	// Check the storage obligation again.
	if len(so.sectorMap) != 1 {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), 1)
	}
	if len(so.sectorRoots) != 1 {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), 1)
	}
	if _, exists := so.sectorMap[appendDataRoot1]; !exists {
		t.Fatal("sectorMap contains wrong root")
	}
	if so.sectorRoots[0] != appendDataRoot1 {
		t.Fatal("sectorRoots contains wrong root")
	}

	// Execute same program again to append another sector.
	appendData2 := randomSectorData() // new random data
	appendDataRoot2 := crypto.MerkleRoot(appendData2)
	instructions, programData, cost, refund, collateral, memory, _ = newAppendProgram(appendData2, true, pt)
	dataLen = uint64(len(programData))
	ics := so.ContractSize()

	// Verify the costs.
	expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime, err = EstimateProgramCosts(pt, instructions, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	err = testCompareCosts(cost, refund, collateral, memory, time, expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs = []Output{
		{
			output{
				NewSize:       ics + modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{appendDataRoot1, appendDataRoot2}),
				Proof:         []crypto.Hash{appendDataRoot1},
			},
			cost.Sub(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit)),
			collateral,
			refund,
		},
	}

	// Execute it.
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) != 1 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 1)
	}
	if len(so.sectorRoots) != 1 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 1)
	}
	// Finalize the program.
	if err := finalize(so); err != nil {
		t.Fatal(err)
	}
	// Check the storage obligation again.
	if len(so.sectorMap) != 2 {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), 2)
	}
	if len(so.sectorRoots) != 2 {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), 2)
	}
	if _, exists := so.sectorMap[appendDataRoot2]; !exists {
		t.Fatal("sectorMap contains wrong root")
	}
	if so.sectorRoots[0] != appendDataRoot1 {
		t.Fatal("sectorRoots contains wrong root")
	}
	if so.sectorRoots[1] != appendDataRoot2 {
		t.Fatal("sectorRoots contains wrong root")
	}
}
