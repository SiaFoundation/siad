package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// newReadSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// ReadSectorInstruction.
func newReadSectorProgram(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool, pt *modules.RPCPriceTable) (modules.Program, ProgramData, Costs, Costs, error) {
	b := newProgramBuilder(pt, uint64(crypto.HashSize)+2*8, 1)
	err := b.AddReadSectorInstruction(length, offset, merkleRoot, merkleProof)
	if err != nil {
		return nil, nil, Costs{}, Costs{}, err
	}
	costs1 := b.Costs
	instructions, programData, finalCosts, err := b.Finish()
	return instructions, programData, costs1, finalCosts, err
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
	instructions, programData, costs1, finalCosts, err := newReadSectorProgram(readLen, 0, root, true, pt)
	if err != nil {
		t.Fatal(err)
	}
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Verify the costs.
	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs1,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, r)
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
	instructions, programData, costs1, finalCosts, err = newReadSectorProgram(length, offset, so.sectorRoots[0], true, pt)
	if err != nil {
		t.Fatal(err)
	}
	r = bytes.NewReader(programData)
	dataLen = uint64(len(programData))

	// Verify the costs.
	err = testCompareProgramCosts(pt, instructions, finalCosts, programData)
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
			costs1,
		},
	}

	// Execute it.
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, finalCosts.ExecutionCost, finalCosts.Collateral, so, dataLen, r)
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
