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

func newDropSectorsInstruction(programData []byte, dataOffset, numSectorsDropped uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency) {
	i := NewDropSectorsInstruction(dataOffset, true)
	binary.LittleEndian.PutUint64(programData[dataOffset:dataOffset+8], numSectorsDropped)
	cost, refund := MDMDropSectorsCost(pt, 2*modules.SectorSize)

	return i, cost, refund
}

// TestProgramWithDropSectors tests executing a program with multiple append and swap
// instructions.
func TestInstructionAppendAndDropSectors(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Construct the program.

	dataLen := 2*modules.SectorSize + 8*2
	programData := make([]byte, dataLen)
	pt := newTestPriceTable()

	instruction1 := NewAppendInstruction(0, false)
	sectorData1 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[:modules.SectorSize], sectorData1)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}
	cost1, refund1 := AppendCost(pt)

	instruction2 := NewAppendInstruction(modules.SectorSize, false)
	sectorData2 := fastrand.Bytes(int(modules.SectorSize))
	copy(programData[modules.SectorSize:2*modules.SectorSize], sectorData2)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}
	cost2, refund2 := AppendCost(pt)

	// Don't drop any sectors.
	instruction3, cost3, refund3 := newDropSectorsInstruction(programData, 2*modules.SectorSize, 0, pt)

	// Drop both sectors at once.
	instruction4, cost4, refund4 := newDropSectorsInstruction(programData, 2*modules.SectorSize+8, 2, pt)

	cost := InitCost(pt, dataLen).Add(cost1).Add(cost2).Add(cost3).Add(cost4)

	// Construct the inputs and expected outputs.
	instructions := []modules.Instruction{
		instruction1, instruction2,
		instruction3, instruction4,
	}
	testOutputs := []Output{
		{
			output{
				NewSize:       modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
				Proof:         []crypto.Hash{},
			},
			cost1, refund1,
		},
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			cost2, refund2,
		},
		// 0 sectors dropped.
		{
			output{
				NewSize:       2 * modules.SectorSize,
				NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
				Proof:         []crypto.Hash{},
			},
			cost3, refund3,
		},
		// 2 sectors dropped.
		{
			output{
				NewSize:       0,
				NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
				Proof:         crypto.MerkleSectorRangeProof(merkleRoots2, 0, 1),
			},
			cost4, refund4,
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
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), testOutput.ExecutionCost.HumanString())
		}
		if !output.PotentialRefund.Equals(testOutput.PotentialRefund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), testOutput.PotentialRefund.HumanString())
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
