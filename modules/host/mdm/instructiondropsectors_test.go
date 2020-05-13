package mdm

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestDropSectorsVerify tests verification of DropSectors input.
func TestDropSectorsVerify(t *testing.T) {
	tests := []struct {
		numDropped, oldNum uint64
		err                error
	}{
		{0, 0, nil},
		{0, 1, nil},
		{1, 1, nil},
		{2, 1, fmt.Errorf("bad input: numSectors (%v) is greater than the number of sectors in the contract (%v)", 2, 1)},
	}
	for _, test := range tests {
		err := dropSectorsVerify(test.numDropped, test.oldNum)
		if err != test.err && err.Error() != test.err.Error() {
			t.Errorf("dropSectorsVerify(%v, %v): expected '%v', got '%v'", test.numDropped, test.oldNum, test.err, err)
		}
	}
}

// TestProgramWithDropSectors tests executing a program with multiple Append and
// DropSectors instructions.
func TestInstructionAppendAndDropSectors(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Construct the program.

	pt := newTestPriceTable()
	tb := newTestBuilder(pt, 6, 3*modules.SectorSize+3*8)

	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	runningValues1 := tb.TestAddAppendInstruction(sectorData1, false)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	runningValues2 := tb.TestAddAppendInstruction(sectorData2, false)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	runningValues3 := tb.TestAddAppendInstruction(sectorData3, false)
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	runningValues4 := tb.TestAddDropSectorsInstruction(0, true)

	// Drop one sector.
	runningValues5 := tb.TestAddDropSectorsInstruction(1, true)

	// Drop two remaining sectors.
	runningValues6 := tb.TestAddDropSectorsInstruction(2, true)

	program, data := tb.Program()
	finalValues := tb.Values()
	dataLen := uint64(len(data))

	err := testCompareProgramValues(pt, program, dataLen, bytes.NewReader(data), finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
				Proof:         []crypto.Hash{},
			},
			runningValues1,
		},
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			runningValues2,
		},
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			runningValues3,
		},
		// 0 sectors dropped.
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			runningValues4,
		},
		// 1 sector dropped.
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{cachedMerkleRoot(merkleRoots2)},
			},
			runningValues5,
		},
		// 2 remaining sectors dropped.
		{
			output{
				NewSize:       0,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
				Proof:         []crypto.Hash{},
			},
			runningValues6,
		},
	}

	// Execute the program.
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
	lastOutput, err := testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// The storage obligation should be unchanged before finalizing the program.
	numSectors := 0
	if len(so.sectorMap) != numSectors {
		t.Fatalf("wrong sectorMap len %v > %v", len(so.sectorMap), numSectors)
	}
	if len(so.sectorRoots) != numSectors {
		t.Fatalf("wrong sectorRoots len %v > %v", len(so.sectorRoots), numSectors)
	}

	// Finalize the program.
	if err := finalizeFn(so); err != nil {
		t.Fatal(err)
	}

	// Budget should be empty now.
	if !budget.Remaining().IsZero() {
		t.Fatal("budget wasn't completely depleted")
	}

	// Update variables.
	numSectors = int(lastOutput.NewSize / modules.SectorSize)

	// Check the storage obligation again.
	if len(so.sectorMap) != numSectors {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), numSectors)
	}
	if len(so.sectorRoots) != numSectors {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), numSectors)
	}
}
