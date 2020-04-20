package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newAppendProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// AppendInstruction.
func newAppendProgram(sectorData []byte, merkleProof bool, pt *modules.RPCPriceTable) (modules.Program, ProgramData, Costs, Costs, error) {
	b := newProgramBuilder(pt, uint64(len(sectorData)), 1)
	err := b.AddAppendInstruction(sectorData, merkleProof)
	if err != nil {
		return nil, nil, Costs{}, Costs{}, err
	}
	costs1 := b.Costs
	instructions, programData, finalCosts, err := b.Finish()
	return instructions, programData, costs1, finalCosts, err
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
	instructions, programData, costs1, finalCosts, err := newAppendProgram(appendData1, true, pt)
	if err != nil {
		t.Fatal(err)
	}
	dataLen := uint64(len(programData))

	// Verify the costs.
	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs1,
		},
	}

	// Execute it.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
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
	if err := finalize(so); err != nil {
		t.Fatal(err)
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
	instructions, programData, costs2, finalCosts, err := newAppendProgram(appendData2, true, pt)
	if err != nil {
		t.Fatal(err)
	}
	programData = appendData2
	dataLen = uint64(len(programData))
	ics := so.ContractSize()

	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs2,
		},
	}

	// Execute it.
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, bytes.NewReader(programData))
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
	if err := finalize(so); err != nil {
		t.Fatal(err)
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
