package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newAppendProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// AppendInstruction.
func newAppendProgram(sectorData []byte, merkleProof bool, pt modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, uint64) {
	instructions := []modules.Instruction{
		NewAppendInstruction(0, merkleProof),
	}

	// Compute cost and used memory.
	cost, refund := AppendCost(pt)
	usedMemory := AppendMemory()
	memoryCost := MemoryCost(pt, usedMemory, TimeAppend+TimeCommit)
	initCost := InitCost(pt, uint64(len(sectorData)))
	cost = cost.Add(memoryCost).Add(initCost)
	return instructions, sectorData, cost, refund, usedMemory
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
	instructions, programData, cost, refund, usedMemory := newAppendProgram(appendData1, true, pt)
	dataLen := uint64(len(programData))
	// Execute it.
	so := newTestStorageObligation(true)
	snapshot := so // TestStorageObligation implements the snapshot interface
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, snapshot, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	// Execute program and count results.
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
		if !output.ExecutionCost.Equals(cost.Sub(MemoryCost(pt, usedMemory, TimeCommit))) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
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
	instructions, programData, cost, refund, usedMemory = newAppendProgram(appendData2, true, pt)
	dataLen = uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, cost, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
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
		if len(output.Proof) != 0 {
			t.Fatalf("expected proof length to be %v but was %v", 0, len(output.Proof))
		}
		if uint64(len(output.Output)) != 0 {
			t.Fatalf("expected output to have len %v but was %v", 0, len(output.Output))
		}
		if !output.ExecutionCost.Equals(cost.Sub(MemoryCost(pt, usedMemory, TimeCommit))) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
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
