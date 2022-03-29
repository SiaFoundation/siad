package mdm

import (
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
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
	duration := types.BlockHeight(fastrand.Uint64n(5))
	tb := newTestProgramBuilder(pt, duration)

	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	tb.AddAppendInstruction(sectorData1, false, duration)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	tb.AddAppendInstruction(sectorData2, false, duration)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	tb.AddAppendInstruction(sectorData3, false, duration)
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	tb.AddDropSectorsInstruction(0, true)

	// Drop one sector.
	tb.AddDropSectorsInstruction(1, true)

	// Drop two remaining sectors.
	tb.AddDropSectorsInstruction(2, true)

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
	so := host.newTestStorageObligation(true)
	finalizeFn, budget, outputs, err := mdm.ExecuteProgramWithBuilderManualFinalize(tb, so, duration, true)
	if err != nil {
		t.Fatal(err)
	}
	lastOutput := outputs[len(outputs)-1]

	// Assert outputs.
	if len(outputs) != len(expectedOutputs) {
		t.Fatalf("expected %v outputs but got %v", len(expectedOutputs), len(outputs))
	}
	for i, output := range outputs {
		expected := expectedOutputs[i]
		if err := output.assert(expected.NewSize, expected.NewMerkleRoot, expected.Proof, expected.Output, nil); err != nil {
			t.Fatal(err)
		}
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
