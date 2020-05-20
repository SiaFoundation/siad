package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
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
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(initialContractSectors)
	root := so.sectorRoots[0]
	outputData, err := host.ReadSector(root)
	if err != nil {
		t.Fatal(err)
	}
	// Use a builder to build the program.
	readLen := modules.SectorSize
	tb := newTestBuilder(pt)
	tb.AddReadSectorInstruction(readLen, 0, so.sectorRoots[0], true)

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert the output.
	err = outputs[0].assert(ics, imr, []crypto.Hash{}, outputData)
	if err != nil {
		t.Fatal(err)
	}
	sectorData := outputs[0].Output

	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset

	// Use a builder to build the program.
	tb = newTestBuilder(pt)
	tb.AddReadSectorInstruction(length, offset, so.sectorRoots[0], true)

	// Execute it.
	outputs, err = mdm.ExecuteProgramWithBuilder(tb, so, false)
	if err != nil {
		t.Fatal(err)
	}

	// Assert the output.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	outputData = sectorData[modules.SectorSize/2:]
	err = outputs[0].assert(ics, imr, proof, outputData)
	if err != nil {
		t.Fatal(err)
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
	readLen := modules.SectorSize

	// Execute it.
	so := newTestStorageObligation(true)
	// Use a builder to build the program.
	tb := newTestBuilder(pt)
	tb.AddReadSectorInstruction(readLen, 0, sectorRoot, true)
	imr := crypto.Hash{}

	// Execute it.
	outputs, err := mdm.ExecuteProgramWithBuilder(tb, so, false)
	if err != nil {
		t.Fatal(err)
	}

	// Check output.
	outputs[0].assert(0, imr, []crypto.Hash{}, sectorData)
}
