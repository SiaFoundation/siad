package mdm

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
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

// newDropSectorsInstruction is a convenience method for creating a single
// DropSectors instruction.
func newDropSectorsInstruction(programData []byte, dataOffset, numSectorsDropped uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := NewDropSectorsInstruction(dataOffset, true)
	binary.LittleEndian.PutUint64(programData[dataOffset:dataOffset+8], numSectorsDropped)

	time := modules.MDMDropSectorsTime(numSectorsDropped)
	cost, refund := modules.MDMDropSectorsCost(pt, numSectorsDropped)
	collateral := modules.MDMDropSectorsCollateral()
	return i, cost, refund, collateral, modules.MDMDropSectorsMemory(), time
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
	programData := make([]byte, dataLen)
	pt := newTestPriceTable()
	initCost := modules.MDMInitCost(pt, dataLen, numInstructions)
	costCalculator := costCalculator{pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory()}

	instruction1, cost, refund, collateral, memory, time := newAppendInstruction(false, 0, pt)
	// Store intermediate costs for comparison at each output step.
	cost1, refund1, collateral1, _ := costCalculator.update(cost, refund, collateral, memory, time)
	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[:modules.SectorSize], sectorData1)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	instruction2, cost, refund, collateral, memory, time := newAppendInstruction(false, modules.SectorSize, pt)
	cost2, refund2, collateral2, _ := costCalculator.update(cost, refund, collateral, memory, time)
	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[modules.SectorSize:2*modules.SectorSize], sectorData2)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	instruction3, cost, refund, collateral, memory, time := newAppendInstruction(false, 2*modules.SectorSize, pt)
	cost3, refund3, collateral3, _ := costCalculator.update(cost, refund, collateral, memory, time)
	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[2*modules.SectorSize:3*modules.SectorSize], sectorData3)
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	instruction4, cost, refund, collateral, memory, time := newDropSectorsInstruction(programData, 3*modules.SectorSize, 0, pt)
	cost4, refund4, collateral4, _ := costCalculator.update(cost, refund, collateral, memory, time)

	// Drop one sector.
	instruction5, cost, refund, collateral, memory, time := newDropSectorsInstruction(programData, 3*modules.SectorSize+8, 1, pt)
	cost5, refund5, collateral5, _ := costCalculator.update(cost, refund, collateral, memory, time)

	// Drop two remaining sectors.
	instruction6, cost, refund, collateral, memory, time := newDropSectorsInstruction(programData, 3*modules.SectorSize+16, 2, pt)
	cost6, refund6, collateral6, memory6 := costCalculator.update(cost, refund, collateral, memory, time)

	cost = cost6.Add(modules.MDMMemoryCost(pt, memory6, modules.MDMTimeCommit))
	collateral = collateral6

	// Construct the program.
	instructions := []modules.Instruction{
		// Append
		instruction1, instruction2, instruction3,
		// DropSectors
		instruction4, instruction5, instruction6,
	}

	// Verify the costs.
	// expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime = EstimateProgramCosts(pt, instructions)
	// testCompareCosts(t, cost, refund, collateral, memory, time, expectedCost, expectedRefund, expectedCollateral, expectedMemory, expectedTime)

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
				Proof:         []crypto.Hash{},
			},
			cost1,
			collateral1,
			refund1,
		},
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			cost2,
			collateral2,
			refund2,
		},
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			cost3,
			collateral3,
			refund3,
		},
		// 0 sectors dropped.
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			cost4,
			collateral4,
			refund4,
		},
		// 1 sector dropped.
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{cachedMerkleRoot(merkleRoots2)},
			},
			cost5,
			collateral5,
			refund5,
		},
		// 2 remaining sectors dropped.
		{
			output{
				NewSize:       0,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
				Proof:         []crypto.Hash{},
			},
			cost6,
			collateral6,
			refund6,
		},
	}

	// Execute the program.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, bytes.NewReader(programData))
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
