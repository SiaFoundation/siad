package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/encoding"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionRevision tests executing a program with a single
// RevisionInstruction.
func TestInstructionRevision(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	so := host.newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(1)

	// Add sector to the host.
	sectorRoot := so.sectorRoots[0]
	_, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Get revision, contract size and root.
	rev := so.RecentRevision()
	ics := rev.NewFileSize
	imr := rev.NewFileMerkleRoot
	var zmr crypto.Hash
	if ics == 0 || imr == zmr {
		t.Fatal("ics and/or were not initialized")
	}

	// Build the program.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	tb := newTestProgramBuilder(pt, duration)
	tb.AddRevisionInstruction()

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert output.
	expectedOutput := encoding.Marshal(modules.MDMInstructionRevisionResponse{
		RevisionTxn: so.RevisionTxn(),
	})
	err = outputs[0].assert(ics, imr, []crypto.Hash{}, expectedOutput, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify revision signature.
	var response modules.MDMInstructionRevisionResponse
	err = encoding.Unmarshal(outputs[0].Output, &response)
	if err != nil {
		t.Fatal(err)
	}
	revisionTxn := response.RevisionTxn
	var signature crypto.Signature
	copy(signature[:], revisionTxn.RenterSignature().Signature)
	hash := revisionTxn.SigHash(0, host.BlockHeight()) // this should be the start height but this works too
	err = crypto.VerifyHash(hash, so.sk.PublicKey(), signature)
	if err != nil {
		t.Fatal(err)
	}
}
