package mdm

import (
	"bytes"
	"context"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// updateRunningCosts is a testing helper function for updating the running
// costs of a program after adding an instruction.
func updateRunningCosts(pt *modules.RPCPriceTable, runningCost, runningRefund, runningCollateral types.Currency, runningMemory uint64, cost, refund, collateral types.Currency, memory, time uint64) (types.Currency, types.Currency, types.Currency, uint64) {
	runningMemory = runningMemory + memory
	memoryCost := modules.MDMMemoryCost(pt, runningMemory, time)
	runningCost = runningCost.Add(memoryCost).Add(cost)
	runningRefund = runningRefund.Add(refund)
	runningCollateral = runningCollateral.Add(collateral)

	return runningCost, runningRefund, runningCollateral, runningMemory
}

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	var r io.Reader
	// Shouldn't be able to execute empty program.
	pt := newTestPriceTable()
	budget := modules.NewBudget(modules.MDMInitCost(pt, 0, 0))
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, budget, types.ZeroCurrency, newTestStorageObligation(true), 0, r)
	if !errors.Contains(err, ErrEmptyProgram) {
		t.Fatal("expected ErrEmptyProgram", err)
	}
}

// TestNewProgramLowInitBudget runs a program that doesn't even have enough funds to init the MDM.
func TestNewProgramLowInitBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	pb := modules.NewProgramBuilder(newTestPriceTable())
	pb.AddHasSectorInstruction(crypto.Hash{})
	program, data := pb.Program()
	r := bytes.NewReader(data)
	// Execute the program.
	pt := newTestPriceTable()
	budget := modules.NewBudget(types.ZeroCurrency)
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, types.ZeroCurrency, newTestStorageObligation(true), 0, r)
	if !errors.Contains(err, modules.ErrMDMInsufficientBudget) {
		t.Fatal("missing error")
	}
}

// TestNewProgramLowBudget runs a program with instructions with insufficient
// funds.
func TestNewProgramLowBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	// Create instruction.
	pt := newTestPriceTable()
	pb := modules.NewProgramBuilder(pt)
	pb.AddReadSectorInstruction(modules.SectorSize, 0, crypto.Hash{}, true)
	instructions, programData := pb.Program()
	_, _, collateral := pb.Cost(true)
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))
	// Execute the program with enough money to init the mdm but not enough
	// money to execute the first instruction.
	cost := modules.MDMInitCost(pt, dataLen, 1)
	budget := modules.NewBudget(cost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, budget, collateral, newTestStorageObligation(true), dataLen, r)
	if err != nil {
		t.Fatal(err)
	}
	// The first output should contain an error.
	numOutputs := 0
	numInsufficientBudgetErrs := 0
	for output := range outputs {
		if err := output.Error; errors.Contains(err, modules.ErrMDMInsufficientBudget) {
			numInsufficientBudgetErrs++
		} else if err != nil {
			t.Fatal(err)
		}
		numOutputs++
	}
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
	}
	if numInsufficientBudgetErrs != 1 {
		t.Fatalf("numInsufficientBudgetErrs was %v but should be %v", numInsufficientBudgetErrs, 1)
	}
	// Finalize should be nil for readonly programs.
	if finalize != nil {
		t.Fatal("finalize should be 'nil' for readonly programs")
	}
}

// TestNewProgramLowCollateralBudget runs a program with instructions with insufficient
// collateral budget.
func TestNewProgramLowCollateralBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	// Create instruction.
	pt := newTestPriceTable()
	instructions, programData, cost, _, _, _ := newAppendProgram(fastrand.Bytes(int(modules.SectorSize)), false, pt)
	budget := modules.NewBudget(cost)
	// Execute the program with no collateral budget.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, budget, types.ZeroCurrency, so, uint64(len(programData)), bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}
	// The first output should contain an error.
	numOutputs := 0
	numInsufficientBudgetErrs := 0
	for output := range outputs {
		if err := output.Error; errors.Contains(err, modules.ErrMDMInsufficientCollateralBudget) {
			numInsufficientBudgetErrs++
		} else if err != nil {
			t.Fatal(err)
		}
		numOutputs++
	}
	if numOutputs != 1 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 1)
	}
	if numInsufficientBudgetErrs != 1 {
		t.Fatalf("numInsufficientBudgetErrs was %v but should be %v", numInsufficientBudgetErrs, 1)
	}
	// Try to finalize program. Should fail.
	if err := finalize(so); err == nil {
		t.Fatal("shouldn't be able to finalize program")
	}
}
