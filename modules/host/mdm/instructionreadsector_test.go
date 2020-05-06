package mdm

import (
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
	pb := modules.NewProgramBuilder()
	pb.AddReadSectorInstruction(readLen, 0, so.sectorRoots[0], true)
	program := pb.Program()
	runningValues, finalValues, err := pb.Values(pt, true)
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
	// Get a new reader.
	program = pb.Program()

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
			runningValues[1],
		},
	}

	// Execute it.
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so)
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
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}

	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset

	// Use a builder to build the program.
	pb = modules.NewProgramBuilder()
	pb.AddReadSectorInstruction(length, offset, so.sectorRoots[0], true)
	program = pb.Program()
	runningValues, finalValues, err = pb.Values(pt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}
	// Get a new reader.
	program = pb.Program()

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
			runningValues[1],
		},
	}

	// Execute it.
	budget = modules.NewBudget(finalValues.ExecutionCost)
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so)
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
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
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
	pb := modules.NewProgramBuilder()
	pb.AddReadSectorInstruction(readLen, 0, sectorRoot, true)
	program := pb.Program()
	runningValues, finalValues, err := pb.Values(pt, true)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}
	// Get a new reader.
	program = pb.Program()

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
			runningValues[1],
		},
	}

	// Execute it.
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so)
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
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
