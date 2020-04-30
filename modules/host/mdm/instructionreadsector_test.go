package mdm

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newReadSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// ReadSectorInstruction.
func newReadSectorProgram(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool, pt *modules.RPCPriceTable) (modules.Program, RunningProgramValues, ProgramValues, error) {
	b := newProgramBuilder()
	b.AddReadSectorInstruction(length, offset, merkleRoot, merkleProof)
	program, runningValues, finalValues, err := b.Finalize(pt)
	return program, runningValues[1], finalValues, err
}

// TestInstructionReadSector tests executing a program with a single
// ReadSectorInstruction.
func TestInstructionReadSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Create a program to read a full sector from the host.
	pt := newTestPriceTable()
	readLen := modules.SectorSize
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(initialContractSectors)
	root := so.sectorRoots[0]
	program, runningValues, finalValues, err := newReadSectorProgram(readLen, 0, root, true, pt)
	if err != nil {
		t.Fatal(err)
	}
	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	outputData, err := host.ReadSector(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedOutputs := []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        outputData,
			},
			runningValues,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, finalValues.ExecutionCost, finalValues.Collateral, so)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	lastOutput, err := testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}
	sectorData := lastOutput.Output

	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}

	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset
	program, runningValues, finalValues, err = newReadSectorProgram(length, offset, so.sectorRoots[0], true, pt)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the values.
	err = testCompareProgramValues(pt, program, finalValues)
	if err != nil {
		t.Fatal(err)
	}

	// Expected outputs.
	proofStart := int(offset) / crypto.SegmentSize
	proofEnd := int(offset+length) / crypto.SegmentSize
	proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
	outputData = sectorData[modules.SectorSize/2:]
	expectedOutputs = []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         proof,
				Output:        outputData,
			},
			runningValues,
		},
	}

	// Execute it.
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, program, finalValues.ExecutionCost, finalValues.Collateral, so)
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// No need to finalize the program since an this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
