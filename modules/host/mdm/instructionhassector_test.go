package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash) ([]modules.Instruction, []byte) {
	instructions := []modules.Instruction{
		NewHasSectorInstruction(0),
	}
	data := make([]byte, crypto.HashSize)
	copy(data[:crypto.HashSize], merkleRoot[:])
	return instructions, data
}

// TestInstructionHasSector tests executing a program with a single
// HasSectorInstruction.
func TestInstructionHasSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a random sector to the host.
	var sectorRoot crypto.Hash
	fastrand.Read(sectorRoot[:])
	_, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Create a program to check for a sector on the host.
	instructions, programData := newHasSectorProgram(sectorRoot)
	dataLen := uint64(len(programData))
	// Execute it.
	pt := newTestPriceTable()
	so := newTestStorageObligation(true)
	so.sectorRoots = make([]crypto.Hash, 1) // initial contract has 1 sector
	cost, refund := HasSectorCost(pt)
	usedMemory := HasSectorMemory()
	memoryCost := MemoryCost(pt, usedMemory, TimeAppend+TimeCommit)
	initCost := InitCost(pt, dataLen)
	cost = cost.Add(memoryCost).Add(initCost)
	fastrand.Read(so.sectorRoots[0][:]) // random initial merkle root
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, InitCost(pt, dataLen).Add(cost), so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	// Check outputs.
	numOutputs := 0
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != so.ContractSize() {
			t.Fatalf("expected contract size to stay the same: %v != %v", so.ContractSize(), output.NewSize)
		}
		if output.NewMerkleRoot != so.MerkleRoot() {
			t.Fatalf("expected merkle root to stay the same: %v != %v", so.MerkleRoot(), output.NewMerkleRoot)
		}
		// Verify proof was created correctly.
		if len(output.Proof) != 0 {
			t.Fatalf("expected proof to have len %v but was %v", 0, len(output.Proof))
		}
		if !bytes.Equal(output.Output, []byte{1}) {
			t.Fatalf("expected returned value to be 1 for 'true' but was %v", output.Output)
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
	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
