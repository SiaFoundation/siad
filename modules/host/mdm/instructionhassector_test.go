package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
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
	tb.TestAddHasSectorInstruction(sectorRoot)

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Expected outputs.
	expectedOutputs := []output{
		{
			NewSize:       ics,
			NewMerkleRoot: imr,
			Proof:         []crypto.Hash{},
			Output:        []byte{1},
		},
	}

	// Execute it.
	_, budget, _, err := tb.AssertOutputs(mdm, so, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	if !budget.Remaining().IsZero() {
		t.Fatalf("budget remaining should be zero but was %v", budget.Remaining().HumanString())
	}
}
