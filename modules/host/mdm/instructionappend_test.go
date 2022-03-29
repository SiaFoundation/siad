package mdm

import (
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
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
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	tb := newTestProgramBuilder(pt, duration)
	tb.AddAppendInstruction(appendData1, true, duration)

	// Execute it.
	so := host.newTestStorageObligation(true)
	finalizeFn, budget, outputs, err := mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the outputs.
	for _, output := range outputs {
		err = output.assert(modules.SectorSize, crypto.MerkleRoot(appendData1), []crypto.Hash{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
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
	appendDataRoot2 := crypto.MerkleRoot(appendData2)
	duration = types.BlockHeight(1)
	tb = newTestProgramBuilder(pt, duration)
	tb.AddAppendInstruction(appendData2, true, duration)
	ics := so.ContractSize()

	// Expected outputs
	expectedOutput := output{
		NewSize:       ics + modules.SectorSize,
		NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{appendDataRoot1, appendDataRoot2}),
		Proof:         []crypto.Hash{appendDataRoot1},
	}

	// Execute it.
	finalizeFn, budget, outputs, err = mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}
	// Assert the outputs.
	for _, output := range outputs {
		err = output.assert(expectedOutput.NewSize, expectedOutput.NewMerkleRoot, expectedOutput.Proof, expectedOutput.Output, nil)
		if err != nil {
			t.Fatal(err)
		}
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
