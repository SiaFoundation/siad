package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/encoding"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestInstructionRevision tests executing a program with a single
// RevisionInstruction.
func TestInstructionRevision(t *testing.T) {
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

	// Get revision.
	rev := so.RecentRevision()

	// Build the program.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	tb := newTestProgramBuilder(pt, duration)
	tb.AddRevisionInstruction()

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	expectedOutput := encoding.Marshal(rev)
	err = outputs[0].assert(ics, imr, []crypto.Hash{}, rev)
	if err != nil {
		t.Fatal(err)
	}
}
