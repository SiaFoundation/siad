package mdm

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
)

// newDropSectorsInstruction is a convenience method for creating a single
// DropSectors instruction.
func newDropSectorsInstruction(programData []byte, dataOffset, numSectorsDropped uint64, runningCost, runningRefund types.Currency, runningMemory uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, uint64) {
	i := NewDropSectorsInstruction(dataOffset, true)
	binary.LittleEndian.PutUint64(programData[dataOffset:dataOffset+8], numSectorsDropped)

	// Compute cost and used memory
	usedMemory := runningMemory + DropSectorsMemory()
	time := TimeDropSectors * numSectorsDropped
	memoryCost := MemoryCost(pt, usedMemory, time)
	instructionCost, refund := modules.MDMDropSectorsCost(pt, numSectorsDropped)
	cost := runningCost.Add(memoryCost).Add(instructionCost)

	return i, cost, runningRefund.Add(refund), usedMemory
}

// TestProgramWithDropSectors tests executing a program with multiple append and swap
// instructions.
func TestInstructionAppendAndDropSectors(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Construct the program.

	dataLen := 3*modules.SectorSize + 8*3
	programData := make([]byte, dataLen)
	pt := newTestPriceTable()
	initCost := modules.MDMInitCost(pt, dataLen)

	instruction1, cost1, refund1, memory1 := newAppendInstruction(false, 0, initCost, types.ZeroCurrency, 0, pt)
	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[:modules.SectorSize], sectorData1)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}

	instruction2, cost2, refund2, memory2 := newAppendInstruction(false, modules.SectorSize, cost1, refund1, memory1, pt)
	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[modules.SectorSize:2*modules.SectorSize], sectorData2)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}

	instruction3, cost3, refund3, memory3 := newAppendInstruction(false, 2*modules.SectorSize, cost2, refund2, memory2, pt)
	sectorData3 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[2*modules.SectorSize:3*modules.SectorSize], sectorData3)
	merkleRoots3 := []crypto.Hash{merkleRoots2[0], merkleRoots2[1], crypto.MerkleRoot(sectorData3)}

	// Don't drop any sectors.
	instruction4, cost4, refund4, memory4 := newDropSectorsInstruction(programData, 3*modules.SectorSize, 0, cost3, refund3, memory3, pt)

	// Drop one sector.
	instruction5, cost5, refund5, memory5 := newDropSectorsInstruction(programData, 3*modules.SectorSize+8, 1, cost4, refund4, memory4, pt)

	instruction6, cost6, refund6, memory6 := newDropSectorsInstruction(programData, 3*modules.SectorSize+16, 2, cost5, refund5, memory5, pt)

	// Drop two remaining sectors.

	cost := cost6.Add(MemoryCost(pt, memory6, TimeCommit))

	// Construct the inputs and expected outputs.
	instructions := []modules.Instruction{
		// Append
		instruction1, instruction2, instruction3,
		// DropSectors
		instruction4, instruction5, instruction6,
	}
	testOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
				Proof:         []crypto.Hash{},
			},
			cost1,
			refund1,
		},
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			cost2,
			refund2,
		},
		{
			output{
				NewSize:       3 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots3),
				Proof:         []crypto.Hash{},
			},
			cost3,
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
			refund6,
		},
	}

	// Execute the program.

	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}

	numOutputs := 0
	var lastOutput Output
	for output := range outputs {
		testOutput := testOutputs[numOutputs]

		if output.Error != testOutput.Error {
			t.Fatalf("expected err %v, got %v", testOutput.Error, output.Error)
		}
		if output.NewSize != testOutput.NewSize {
			t.Fatalf("expected contract size %v, got %v", testOutput.NewSize, output.NewSize)
		}
		if output.NewMerkleRoot != testOutput.NewMerkleRoot {
			t.Fatalf("expected merkle root %v, got %v", testOutput.NewMerkleRoot, output.NewMerkleRoot)
		}
		// Check proof.
		if len(output.Proof) != len(testOutput.Proof) {
			t.Fatalf("expected proof to have length %v but was %v", len(testOutput.Proof), len(output.Proof))
		}
		for i, proof := range output.Proof {
			if proof != testOutput.Proof[i] {
				t.Fatalf("expected proof %v, got %v", proof, output.Proof[i])
			}
		}
		// Check data.
		if len(output.Output) != len(testOutput.Output) {
			t.Fatalf("expected returned data to have length %v but was %v", len(testOutput.Output), len(output.Output))
		}
		if !output.ExecutionCost.Equals(testOutput.ExecutionCost) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost, testOutput.ExecutionCost)
		}
		if !output.PotentialRefund.Equals(testOutput.PotentialRefund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund, testOutput.PotentialRefund)
		}

		numOutputs++
		lastOutput = output
	}
	if numOutputs != len(testOutputs) {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, len(testOutputs))
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
	if err := finalize(); err != nil {
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
