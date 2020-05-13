package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestInstructionSingleAppend tests executing a program with a single
// AppendInstruction.
func TestInstructionSingleAppend(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to append a full sector to a storage obligation.
	appendData1 := randomSectorData()
	appendDataRoot1 := crypto.MerkleRoot(appendData1)
	dataLen := uint64(len(appendData1))
	pt := newTestPriceTable()
	tb := newTestBuilder(pt, 1, dataLen)
	runningValues1 := tb.TestAddAppendInstruction(appendData1, true)
	program, data := tb.Program()
	finalValues := tb.Values()

	// Verify the values.
	err := testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
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
			runningValues1,
		},
	}

	// Execute it.
	so := newTestStorageObligation(true)
	budget := modules.NewBudget(finalValues.ExecutionCost)
	finalizeFn, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, so, dataLen, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if finalizeFn == nil {
		t.Fatal("could not retrieve finalizeFn function")
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) > 0 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 0)
	}
	if len(so.sectorRoots) > 0 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 0)
	}
	// Finalize the program.
	if err := finalizeFn(so); err != nil {
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
	dataLen = uint64(len(appendData2))
	appendDataRoot2 := crypto.MerkleRoot(appendData2)
	tb = newTestBuilder(pt, 1, dataLen)
	runningValues1 = tb.TestAddAppendInstruction(appendData2, true)
	program, data = tb.Program()
	finalValues = tb.Values()
	ics := so.ContractSize()

	err = testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
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

	// The storage obligation should be unchanged before finalizing the program.
	if len(so.sectorMap) != 1 {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), 1)
	}
	if len(so.sectorRoots) != 1 {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), 1)
	}
	// Finalize the program.
	if err := finalizeFn(so); err != nil {
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
