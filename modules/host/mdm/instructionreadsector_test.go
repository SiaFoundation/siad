package mdm

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
)

// TestInstructionReadSector tests executing a program with a single
// ReadSectorInstruction.
func TestInstructionReadSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Prepare a priceTable.
	pt := newTestPriceTable()
	// Prepare storage obligation.
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(initialContractSectors)
	// Use a builder to build the program.
	readLen := modules.SectorSize
	pb := modules.NewProgramBuilder(pt)
	pb.AddReadSectorInstruction(readLen, 0, so.sectorRoots[0], true)
	instructions, programData := pb.Program()
	cost, refund, collateral := pb.Cost(true)
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))

	// Execute it.
	ics := so.ContractSize()
	imr := so.MerkleRoot()
	budget := modules.NewBudget(cost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, budget, collateral, so, dataLen, r)
	if err != nil {
		t.Fatal(err)
	}
	// There should be one output since there was one instruction.
	numOutputs := 0
	var sectorData []byte
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != ics {
			t.Fatalf("expected contract size to stay the same: %v != %v", ics, output.NewSize)
		}
		if output.NewMerkleRoot != imr {
			t.Fatalf("expected merkle root to stay the same: %v != %v", imr, output.NewMerkleRoot)
		}
		if len(output.Proof) != 0 {
			t.Fatalf("expected proof length to be %v but was %v", 0, len(output.Proof))
		}
		if uint64(len(output.Output)) != modules.SectorSize {
			t.Fatalf("expected returned data to have length %v but was %v", modules.SectorSize, len(output.Output))
		}
		if !output.ExecutionCost.Equals(cost) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
		}
		if !budget.Remaining().Equals(cost.Sub(output.ExecutionCost)) {
			t.Fatalf("budget should be equal to the initial budget minus the execution cost: %v != %v",
				budget.Remaining().HumanString(), cost.Sub(output.ExecutionCost).HumanString())
		}
		if !output.AdditionalCollateral.Equals(collateral) {
			t.Fatalf("collateral doesnt't match expected collateral: %v != %v", output.AdditionalCollateral.HumanString(), collateral.HumanString())
		}
		if !output.PotentialRefund.Equals(refund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), refund.HumanString())
		}
		sectorData = output.Output
		numOutputs++
	}
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
	}
	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
	// Create a program to read half a sector from the host.
	offset := modules.SectorSize / 2
	length := offset
	// Use a builder to build the program.
	pb = modules.NewProgramBuilder(pt)
	pb.AddReadSectorInstruction(length, offset, so.sectorRoots[0], true)
	instructions, programData = pb.Program()
	cost, refund, collateral = pb.Cost(true)
	r = bytes.NewReader(programData)
	dataLen = uint64(len(programData))
	// Execute it.
	budget = modules.NewBudget(cost)
	finalize, outputs, err = mdm.ExecuteProgram(context.Background(), pt, instructions, budget, collateral, so, dataLen, r)
	if err != nil {
		t.Fatal(err)
	}
	// There should be one output since there was one instructions.
	numOutputs = 0
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != ics {
			t.Fatalf("expected contract size to stay the same: %v != %v", ics, output.NewSize)
		}
		if output.NewMerkleRoot != imr {
			t.Fatalf("expected merkle root to stay the same: %v != %v", imr, output.NewMerkleRoot)
		}
		proofStart := int(offset) / crypto.SegmentSize
		proofEnd := int(offset+length) / crypto.SegmentSize
		proof := crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
		if !reflect.DeepEqual(proof, output.Proof) {
			t.Fatal("proof doesn't match expected proof")
		}
		if !bytes.Equal(output.Output, sectorData[modules.SectorSize/2:]) {
			t.Fatal("output should match the second half of the sector data")
		}
		if !output.ExecutionCost.Equals(cost) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
		}
		if !budget.Remaining().Equals(cost.Sub(output.ExecutionCost)) {
			t.Fatalf("budget should be equal to the initial budget minus the execution cost: %v != %v",
				budget.Remaining().HumanString(), cost.Sub(output.ExecutionCost).HumanString())
		}
		if !output.AdditionalCollateral.Equals(collateral) {
			t.Fatalf("collateral doesnt't match expected collateral: %v != %v", output.AdditionalCollateral.HumanString(), collateral.HumanString())
		}
		if !output.PotentialRefund.Equals(refund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), refund.HumanString())
		}
		numOutputs++
	}
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
	}
	// No need to finalize the program since an this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}

// TestInstructionReadOutsideSector tests reading a sector from outside the
// storage obligation.
func TestInstructionReadOutsideSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a sector root to the host but not to the SO.
	sectorRoot := randomSectorRoots(1)[0]
	sectorData, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Create a program to read a full sector from the host.
	pt := newTestPriceTable()
	readLen := modules.SectorSize

	// Execute it.
	so := newTestStorageObligation(true)
	// Use a builder to build the program.
	pb := modules.NewProgramBuilder(pt)
	pb.AddReadSectorInstruction(readLen, 0, sectorRoot, true)
	instructions, programData := pb.Program()
	cost, refund, collateral := pb.Cost(true)
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))
	// Execute it.
	budget := modules.NewBudget(cost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, budget, collateral, so, dataLen, r)
	if err != nil {
		t.Fatal(err)
	}
	// There should be one output since there was one instruction.
	numOutputs := 0
	imr := crypto.Hash{}
	for output := range outputs {
		if err := output.Error; err != nil {
			t.Fatal(err)
		}
		if output.NewSize != 0 {
			t.Fatalf("expected contract size to stay the same: %v != %v", 0, output.NewSize)
		}
		if output.NewMerkleRoot != imr {
			t.Fatalf("expected merkle root to stay the same: %v != %v", imr, output.NewMerkleRoot)
		}
		if len(output.Proof) != 0 {
			t.Fatalf("expected proof length to be %v but was %v", 0, len(output.Proof))
		}
		if !bytes.Equal(output.Output, sectorData) {
			t.Fatal("output data doesn't match")
		}
		if !output.ExecutionCost.Equals(cost) {
			t.Fatalf("execution cost doesn't match expected execution cost: %v != %v", output.ExecutionCost.HumanString(), cost.HumanString())
		}
		if !budget.Remaining().Equals(cost.Sub(output.ExecutionCost)) {
			t.Fatalf("budget should be equal to the initial budget minus the execution cost: %v != %v",
				budget.Remaining().HumanString(), cost.Sub(output.ExecutionCost).HumanString())
		}
		if !output.AdditionalCollateral.Equals(collateral) {
			t.Fatalf("collateral doesnt't match expected collateral: %v != %v", output.AdditionalCollateral.HumanString(), collateral.HumanString())
		}
		if !output.PotentialRefund.Equals(refund) {
			t.Fatalf("refund doesn't match expected refund: %v != %v", output.PotentialRefund.HumanString(), refund.HumanString())
		}
		sectorData = output.Output
		numOutputs++
	}
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
	}
	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
