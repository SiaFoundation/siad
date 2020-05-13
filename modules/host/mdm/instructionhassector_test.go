package mdm

import (
	"bytes"
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
	tb := newTestBuilder(pt, 1, crypto.HashSize)
	runningValues1 := tb.TestAddHasSectorInstruction(sectorRoot)
	program, data := tb.Program()
	finalValues := tb.Values()
	dataLen := uint64(len(data))

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the values.
	err = testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
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
			runningValues1,
		},
	}

	// Execute it.
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalizeFn, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so, dataLen, bytes.NewReader(data))
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
	if finalizeFn != nil {
		t.Fatal("finalizeFn callback should be nil for readonly program")
	}
}
