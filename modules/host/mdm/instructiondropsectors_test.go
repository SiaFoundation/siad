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

	numAppend, instrLenAppend := uint64(3), modules.SectorSize
	numDropSectors, instrLenDropSectors := uint64(3), uint64(8)
	numInstructions := numAppend + numDropSectors
	dataLen := numAppend*instrLenAppend + numDropSectors*instrLenDropSectors
	pt := newTestPriceTable()
	b := newProgramBuilder(pt, dataLen, numInstructions)

	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	err := b.AddAppendInstruction(sectorData1, false)
	if err != nil {
		t.Fatal(err)
	}
	costs1 := b.Costs
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	err = b.AddAppendInstruction(sectorData2, false)
	if err != nil {
		t.Fatal(err)
	}
	costs2 := b.Costs
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	err = b.AddAppendInstruction(sectorData3, false)
	if err != nil {
		t.Fatal(err)
	}
	costs3 := b.Costs
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	err = b.AddDropSectorsInstruction(0, true)
	if err != nil {
		t.Fatal(err)
	}
	costs4 := b.Costs

	// Drop one sector.
	err = b.AddDropSectorsInstruction(1, true)
	if err != nil {
		t.Fatal(err)
	}
	costs5 := b.Costs

	// Drop two remaining sectors.
	err = b.AddDropSectorsInstruction(2, true)
	if err != nil {
		t.Fatal(err)
	}
	costs6 := b.Costs

	instructions, programData, finalCosts, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}

	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs1,
		},
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			costs2,
		},
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			costs3,
		},
		// 0 sectors dropped.
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			costs4,
		},
		// 1 sector dropped.
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{cachedMerkleRoot(merkleRoots2)},
			},
			costs5,
		},
		// 2 remaining sectors dropped.
		{
			output{
				NewSize:       0,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
				Proof:         []crypto.Hash{},
			},
			costs6,
		},
	}

	// Execute the program.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
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
	if err := finalize(so); err != nil {
		t.Fatal(err)
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
