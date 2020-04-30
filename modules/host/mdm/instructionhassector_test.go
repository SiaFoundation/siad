package mdm

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash, pt *modules.RPCPriceTable) (modules.Program, RunningProgramValues, ProgramValues, error) {
	b := newProgramBuilder()
	b.AddHasSectorInstruction(merkleRoot)
	program, runningValues, finalValues, err := b.Finalize(pt)
	return program, runningValues[1], finalValues, err
}

// TestInstructionHasSector tests executing a program with a single
// HasSectorInstruction.
func TestInstructionHasSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a random sector to the host.
	sectorRoot := randomSector()
	_, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Create a program to check for a sector on the host.
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(1)
	sectorRoot = so.sectorRoots[0]
	pt := newTestPriceTable()
	program, runningValues, finalValues, err := newHasSectorProgram(sectorRoot, pt)
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
			runningValues,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, finalValues.ExecutionCost, finalValues.Collateral, so)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
