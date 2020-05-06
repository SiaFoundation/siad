package mdm

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
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
	pb := modules.NewProgramBuilder()
	pb.AddHasSectorInstruction(sectorRoot)
	program := pb.Program()
	runningValues, finalValues, err := pb.Values(pt, true)
	if err != nil {
		t.Fatal(err)
	}

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        []byte{1},
			},
			runningValues[1],
		},
	}

	// Execute it.
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}
	if !budget.Remaining().IsZero() {
		t.Fatalf("budget remaining should be zero but was %v", budget.Remaining().HumanString())
	}

	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
