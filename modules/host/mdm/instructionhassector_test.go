package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash, pt modules.RPCPriceTable) (Instructions, ProgramData, Costs, Costs, error) {
	b := newProgramBuilder(pt, uint64(crypto.HashSize), 1)
	err := b.AddHasSectorInstruction(merkleRoot)
	if err != nil {
		return nil, nil, Costs{}, Costs{}, err
	}
	costs1 := b.Costs
	instructions, programData, finalCosts, err := b.Finish()
	return instructions, programData, costs1, finalCosts, err
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
	instructions, programData, costs1, finalCosts, err := newHasSectorProgram(sectorRoot, pt)
	if err != nil {
		t.Fatal(err)
	}
	dataLen := uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the costs.
	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs1,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, bytes.NewReader(programData))
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
