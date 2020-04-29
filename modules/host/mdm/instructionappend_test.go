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
func newAppendInstruction(merkleProof bool, dataOffset uint64, pt modules.RPCPriceTable, duration types.BlockHeight) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := NewAppendInstruction(dataOffset, merkleProof)
	cost, refund := modules.MDMAppendCost(pt, duration)
	collateral := modules.MDMAppendCollateral(pt)
	return i, cost, refund, collateral, modules.MDMAppendMemory(), modules.MDMTimeAppend
}

// newAppendProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// AppendInstruction.
func newAppendProgram(sectorData []byte, merkleProof bool, pt modules.RPCPriceTable, duration types.BlockHeight) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64) {
	initCost := modules.MDMInitCost(pt, uint64(len(sectorData)), 1)
	i, cost, refund, collateral, memory, time := newAppendInstruction(merkleProof, 0, pt, duration)
	instructions := []modules.Instruction{i}
	cost, refund, collateral, memory = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), cost, refund, collateral, memory, time)
	cost = cost.Add(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit))
	return instructions, sectorData, cost, refund, collateral, memory
}

// TestInstructionSingleAppend tests executing a program with a single
// AppendInstruction.
func TestInstructionSingleAppend(t *testing.T) {
	host := newTestHost()
	so := newTestStorageObligation(true)
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to append a full sector to a storage obligation.
	appendData1 := randomSectorData()
	appendDataRoot1 := crypto.MerkleRoot(appendData1)
	pt := newTestPriceTable()
	duration := types.BlockHeight(2)
	instructions, programData, cost, refund, collateral, usedMemory := newAppendProgram(appendData1, true, pt, duration)
	dataLen := uint64(len(programData))
	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, duration, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	// Count results.
	numOutputs := 0
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != modules.SectorSize {
			t.Fatalf("expected contract size should increase by a sector size: %v != %v", modules.SectorSize, output.NewSize)
		}
		if output.NewMerkleRoot != crypto.MerkleRoot(appendData1) {
			t.Fatalf("expected merkle root to be root of appended sector: %v != %v", crypto.Hash{}, output.NewMerkleRoot)
		}
		if len(output.Proof) != 0 {
			t.Fatalf("expected proof length to be %v but was %v", 0, len(output.Proof))
		}
		if uint64(len(output.Output)) != 0 {
			t.Fatalf("expected output to have len %v but was %v", 0, len(output.Output))
		}
		if !output.ExecutionCost.Equals(cost.Sub(modules.MDMMemoryCost(pt, usedMemory, modules.MDMTimeCommit))) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
		}
		if !output.AdditionalCollateral.Equals(collateral) {
			t.Fatalf("collateral doesnt't match expected collateral: %v != %v", output.AdditionalCollateral.HumanString(), collateral.HumanString())
		}
		if !output.PotentialRefund.Equals(refund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), refund.HumanString())
		}
		numOutputs++
	}
	// There should be one output since there was one instruction.
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
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
	duration = types.BlockHeight(1)
	instructions, programData, cost, refund, collateral, usedMemory = newAppendProgram(appendData2, true, pt, duration)
	dataLen = uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, duration, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	// Count results.
	numOutputs = 0
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != ics+modules.SectorSize {
			t.Fatalf("expected contract size should increase by a sector size: %v != %v", ics+modules.SectorSize, output.NewSize)
		}
		if output.NewMerkleRoot != cachedMerkleRoot([]crypto.Hash{appendDataRoot1, appendDataRoot2}) {
			t.Fatalf("expected merkle root to be root of appended sector: %v != %v", imr, output.NewMerkleRoot)
		}
		if len(output.Proof) != 1 {
			t.Fatalf("expected proof length to be %v but was %v", 1, len(output.Proof))
		}
		if output.Proof[0] != appendDataRoot1 {
			t.Logf("proof should just be hash %v but was %v", appendDataRoot1, output.Proof[0])
		}
		if uint64(len(output.Output)) != 0 {
			t.Fatalf("expected output to have len %v but was %v", 0, len(output.Output))
		}
		if !output.ExecutionCost.Equals(cost.Sub(modules.MDMMemoryCost(pt, usedMemory, modules.MDMTimeCommit))) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
		}
		if !output.AdditionalCollateral.Equals(collateral) {
			t.Fatalf("collateral doesnt't match expected collateral: %v != %v", output.AdditionalCollateral.HumanString(), collateral.HumanString())
		}
		if !output.PotentialRefund.Equals(refund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), refund.HumanString())
		}
		numOutputs++
	}
	// There should be one output since there was one instruction.
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
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
