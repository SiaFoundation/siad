package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestInstructionReadSector tests executing a program with a single
// ReadSectorInstruction.
func TestInstructionReadSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Prepare a priceTable.
	pt := newTestPriceTable()
	// Prepare storage obligation.
	so := host.newTestStorageObligation(true)
	so.AddRandomSectors(initialContractSectors)
	root := so.sectorRoots[0]
	outputData, err := host.ReadSector(root)
	if err != nil {
		t.Fatal(err)
	}
	duration := types.BlockHeight(fastrand.Uint64n(5))
	// Use a builder to build the program.
	readLen := modules.SectorSize
	tb := newTestProgramBuilder(pt, duration)
	tb.AddReadSectorInstruction(readLen, 0, so.sectorRoots[0], true)

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert the output.
	err = outputs[0].assert(ics, imr, []crypto.Hash{}, outputData, nil)
	if err != nil {
		t.Fatal(err)
	}
	sectorData := outputs[0].Output

	// Create a program to read up to half a sector from the host.
	offset := modules.SectorSize / 2 // start in the middle
	// Read up to half a sector.
	numSegments := fastrand.Uint64n(modules.SectorSize/2/crypto.SegmentSize) + 1
	length := numSegments * crypto.SegmentSize

	// Use a builder to build the program.
	tb = newTestProgramBuilder(pt, duration)
	tb.AddReadSectorInstruction(length, offset, root, true)

	// Execute it.
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, duration, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert the output.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	outputData = sectorData[offset:][:length]
	err = outputs[0].assert(ics, imr, proof, outputData, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Verify proof.
	ok := crypto.VerifyRangeProof(outputs[0].Output, outputs[0].Proof, proofStart, proofEnd, root)
	if !ok {
		t.Fatal("failed to verify proof")
	}
}

// TestInstructionReadOutsideSector tests reading a sector from outside the
// storage obligation.
func TestInstructionReadOutsideSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a sector root to the host but not to the SO.
	sectorRoot := randomSectorRoots(1)[0]
	sectorData, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Create a program to read a full sector from the host.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	readLen := modules.SectorSize

	// Execute it.
	so := host.newTestStorageObligation(true)
	// Use a builder to build the program.
	tb := newTestProgramBuilder(pt, duration)
	tb.AddReadSectorInstruction(readLen, 0, sectorRoot, true)
	imr := crypto.Hash{}

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, duration, false)
	if err != nil {
		t.Fatal(err)
	}

	// Check output.
	outputs[0].assert(0, imr, []crypto.Hash{}, sectorData, nil)
}
