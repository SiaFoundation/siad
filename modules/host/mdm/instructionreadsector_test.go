package mdm

import (
	"bytes"
	"context"
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
	// Use a builder to build the program.
	readLen := modules.SectorSize
	tb := newTestBuilder(pt, 1, 16+crypto.HashSize)
	runningValues1 := tb.TestAddReadSectorInstruction(readLen, 0, so.sectorRoots[0], true)
	program, data := tb.Program()
	finalValues := tb.Values()
	dataLen := uint64(len(data))

	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the values.
	err := testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	outputData, err := host.ReadSector(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedOutputs := []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        outputData,
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
	lastOutput, err := testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}
	sectorData := lastOutput.Output

	// No need to finalize the program since this program is readonly.
	if finalizeFn != nil {
		t.Fatal("finalizeFn callback should be nil for readonly program")
	}

	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset

	// Use a builder to build the program.
	tb = newTestBuilder(pt, 1, 16+crypto.HashSize)
	runningValues1 = tb.TestAddReadSectorInstruction(length, offset, so.sectorRoots[0], true)
	program, data = tb.Program()
	finalValues = tb.Values()
	dataLen = uint64(len(data))

	// Verify the values.
	err = testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	outputData = sectorData[modules.SectorSize/2:]
	expectedOutputs = []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         proof,
				Output:        outputData,
			},
			runningValues1,
		},
	}

	// Execute it.
	budget = modules.NewBudget(finalValues.ExecutionCost)
	finalizeFn, outputs, err = mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so, dataLen, bytes.NewReader(data))
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

	// No need to finalize the program since an this program is readonly.
	if finalizeFn != nil {
		t.Fatal("finalizeFn callback should be nil for readonly program")
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
	tb := newTestBuilder(pt, 1, 16+crypto.HashSize)
	runningValues1 := tb.TestAddReadSectorInstruction(readLen, 0, sectorRoot, true)
	program, data := tb.Program()
	finalValues := tb.Values()
	dataLen := uint64(len(data))

	// Verify the values.
	err = testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
	if err != nil {
		t.Fatal(err)
	}

	imr := crypto.Hash{}

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       0,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        sectorData,
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
