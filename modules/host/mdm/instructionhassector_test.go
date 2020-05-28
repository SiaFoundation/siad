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
	tb := newTestProgramBuilder(pt)
	tb.AddHasSectorInstruction(sectorRoot)

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	err = outputs[0].assert(ics, imr, []crypto.Hash{}, []byte{1})
	if err != nil {
		t.Fatal(err)
	}
}
