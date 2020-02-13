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

func newDropSectorsInstruction(programData []byte, dataOffset, numSectorsDropped uint64, cost types.Currency, pt modules.RPCPriceTable) (modules.Instruction, types.Currency) {
	i := NewDropSectorsInstruction(dataOffset, true)
	binary.LittleEndian.PutUint64(programData[dataOffset:dataOffset+8], dataOffset)
	cost = cost.Add(MDMDropSectorsCost(pt, 2*modules.SectorSize))

	return i, cost
}

// TestProgramWithDropSectors tests executing a program with multiple append and swap
// instructions.
func TestInstructionAppendAndDropSectors(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	ss := modules.SectorSize

	// Construct the program.

	dataLen := 2*ss + 8*2
	programData := make([]byte, dataLen)
	pt := newTestPriceTable()
	cost := InitCost(pt, dataLen)

	instruction1 := NewAppendInstruction(0, true)
	sectorData1 := fastrand.Bytes(int(ss))
	copy(programData[:ss], sectorData1)
	merkleRoots1 := []crypto.Hash{crypto.MerkleRoot(sectorData1)}
	// TODO: Use AppendCost
	cost = cost.Add(WriteCost(pt, 0))

	instruction2 := NewAppendInstruction(ss, true)
	sectorData2 := fastrand.Bytes(int(ss))
	copy(programData[ss:2*ss], sectorData2)
	merkleRoots2 := []crypto.Hash{merkleRoots1[0], crypto.MerkleRoot(sectorData2)}
	// TODO: Use AppendCost
	cost = cost.Add(WriteCost(pt, ss))

	// Don't drop any sectors.
	instruction3, cost := newDropSectorsInstruction(programData, 2*ss, 0, cost, pt)

	// Drop both sectors at once.
	instruction4, cost := newDropSectorsInstruction(programData, 2*ss, 2, cost, pt)

	// Construct the inputs and expected outputs.
	instructions := []modules.Instruction{instruction1, instruction2, instruction3, instruction4}
	testOutputs := []Output{
		{
			NewSize:       ss,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots1),
		},
		{
			NewSize:       2 * ss,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
		},
		{
			NewSize:       2 * ss,
			NewMerkleRoot: cachedMerkleRoot(merkleRoots2),
		},
		{
			NewSize:       0,
			NewMerkleRoot: cachedMerkleRoot([]crypto.Hash{}),
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
		// print("Checking output ", numOutputs, "\n")

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
		// TODO: check Proof
		if len(output.Output) != len(testOutput.Output) {
			t.Fatalf("expected returned data to have length %v but was %v", len(testOutput.Output), len(output.Output))
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
	numSectors = int(lastOutput.NewSize / ss)

	// Check the storage obligation again.
	if len(so.sectorMap) != numSectors {
		t.Fatalf("wrong sectorMap len %v != %v", len(so.sectorMap), numSectors)
	}
	if len(so.sectorRoots) != numSectors {
		t.Fatalf("wrong sectorRoots len %v != %v", len(so.sectorRoots), numSectors)
	}
}
