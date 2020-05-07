package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestInstructionHasSector tests executing a program with a single
// HasSectorInstruction.
func TestInstructionHasSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to check for a sector on the host.
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(1)

	// Add sector to the host.
	sectorRoot := so.sectorRoots[0]
	_, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Build the program.
	pt := newTestPriceTable()
	pb := modules.NewProgramBuilder(pt)
	pb.AddHasSectorInstruction(sectorRoot)
	instructions, programData := pb.Program()
	cost, refund, collateral := pb.Cost(true)
	dataLen := uint64(len(programData))

	// Execute it.
	budget := modules.NewBudget(cost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, budget, collateral, so, dataLen, bytes.NewReader(programData))
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
			t.Fatalf("expected returned value to be [1] for 'true' but was %v", output.Output)
		}
		if !output.ExecutionCost.Equals(cost.Sub(pb.BandwidthCost())) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.Sub(pb.BandwidthCost()).HumanString())
		}
		if !budget.Remaining().Equals(cost.Sub(output.ExecutionCost)) {
			t.Fatalf("budget should be equal to the initial budget minus the execution cost: %v != %v",
				budget.Remaining().HumanString(), cost.Sub(output.ExecutionCost).HumanString())
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
	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
