package mdm

import (
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
	tb.TestAddAppendInstruction(sectorData1, false)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	tb.TestAddAppendInstruction(sectorData2, false)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	tb.TestAddAppendInstruction(sectorData3, false)
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	tb.TestAddDropSectorsInstruction(0, true)

	// Drop one sector.
	tb.TestAddDropSectorsInstruction(1, true)

	// Drop two remaining sectors.
	tb.TestAddDropSectorsInstruction(2, true)

	// Expected outputs.
	expectedOutputs := []output{
		{
			NewSize:       modules.SectorSize,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
			Proof:         []crypto.Hash{},
		},
		{
			NewSize:       2 * modules.SectorSize,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
			Proof:         []crypto.Hash{},
		},
		{
			NewSize:       3 * modules.SectorSize,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
			Proof:         []crypto.Hash{},
		},
		// 0 sectors dropped.
		{
			NewSize:       3 * modules.SectorSize,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
			Proof:         []crypto.Hash{},
		},
		// 1 sector dropped.
		{
			NewSize:       2 * modules.SectorSize,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
			Proof:         []crypto.Hash{cachedMerkleRoot(merkleRoots2)},
		},
		// 2 remaining sectors dropped.
		{
			NewSize:       0,
			NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
			Proof:         []crypto.Hash{},
		},
	}

	// Execute the program.
	so := newTestStorageObligation(true)
	finalizeFn, budget, lastOutput, err := tb.AssertOutputs(mdm, so, expectedOutputs)
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
