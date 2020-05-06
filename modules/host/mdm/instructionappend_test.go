package mdm

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newAppendProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// AppendInstruction.
func newAppendProgram(sectorData []byte, merkleProof bool, pt *modules.RPCPriceTable) (modules.Program, modules.RunningProgramValues, modules.ProgramValues, error) {
	pb := modules.NewProgramBuilder()
	err := pb.AddAppendInstruction(sectorData, merkleProof)
	if err != nil {
		return modules.Program{}, modules.RunningProgramValues{}, modules.ProgramValues{}, err
	}
	program := pb.Program()
	runningValues, finalValues, err := pb.Values(pt, true)
	return program, runningValues[1], finalValues, err
}

// TestInstructionSingleAppend tests executing a program with a single
// AppendInstruction.
func TestInstructionSingleAppend(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to append a full sector to a storage obligation.
	appendData1 := randomSectorData()
	appendDataRoot1 := crypto.MerkleRoot(appendData1)
	pt := newTestPriceTable()
	program, runningValues, finalValues, err := newAppendProgram(appendData1, true, pt)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: crypto.MerkleRoot(appendData1),
				Proof:         []crypto.Hash{},
			},
			runningValues,
		},
	}

	// Execute it.
	so := newTestStorageObligation(true)
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so)
	if err != nil {
		t.Fatal(err)
	}
	if finalize == nil {
		t.Fatal("could not retrieve finalize function")
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}
	if !budget.Remaining().IsZero() {
		t.Fatalf("budget remaining should be zero but was %v", budget.Remaining().HumanString())
	}

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) > 0 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 0)
	}
	if len(so.sectorRoots) > 0 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 0)
	}
	// Finalize the program.
	if err := finalize(so); err != nil {
		t.Fatal(err)
	}
	// Budget should be empty now.
	if !budget.Remaining().IsZero() {
		t.Fatal("budget wasn't completely depleted")
	}
	// Check the storage obligation again.
	if len(so.sectorMap) != 1 {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), 1)
	}
	if len(so.sectorRoots) != 1 {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), 1)
	}
	if _, exists := so.sectorMap[appendDataRoot1]; !exists {
		t.Fatal("sectorMap contains wrong root")
	}
	if so.sectorRoots[0] != appendDataRoot1 {
		t.Fatal("sectorRoots contains wrong root")
	}

	// Execute same program again to append another sector.
	appendData2 := randomSectorData() // new random data
	appendDataRoot2 := crypto.MerkleRoot(appendData2)
	program, runningValues, finalValues, err = newAppendProgram(appendData2, true, pt)
	if err != nil {
		t.Fatal(err)
	}
	ics := so.ContractSize()

	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs = []Output{
		{
			output{
				NewSize:       ics + modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{appendDataRoot1, appendDataRoot2}),
				Proof:         []crypto.Hash{appendDataRoot1},
			},
			runningValues,
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

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) != 1 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 1)
	}
	if len(so.sectorRoots) != 1 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 1)
	}
	// Finalize the program.
	if err := finalize(so); err != nil {
		t.Fatal(err)
	}
	// Budget should be empty now.
	if !budget.Remaining().IsZero() {
		t.Fatal("budget wasn't completely depleted")
	}
	// Check the storage obligation again.
	if len(so.sectorMap) != 2 {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), 2)
	}
	if len(so.sectorRoots) != 2 {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), 2)
	}
	if _, exists := so.sectorMap[appendDataRoot2]; !exists {
		t.Fatal("sectorMap contains wrong root")
	}
	if so.sectorRoots[0] != appendDataRoot1 {
		t.Fatal("sectorRoots contains wrong root")
	}
	if so.sectorRoots[1] != appendDataRoot2 {
		t.Fatal("sectorRoots contains wrong root")
	}
}
