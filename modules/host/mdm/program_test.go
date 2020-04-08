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
func updateRunningCosts(pt modules.RPCPriceTable, runningCost, runningRefund, runningCollateral types.Currency, runningMemory uint64, cost, refund, collateral types.Currency, memory, time uint64) (types.Currency, types.Currency, types.Currency, uint64) {
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
	// Execute the program.
	pt := newTestPriceTable()
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, modules.MDMInitCost(pt, 0), types.ZeroCurrency, newTestStorageObligation(true), 0, r)
	if err != nil {
		t.Fatal(err)
	}
	// There should be no outputs since there were no instructions.
	numOutputs := 0
	for range outputs {
		numOutputs++
	}
	if numOutputs > 0 {
		t.Fatalf("numOutputs was %v but should be %v", numOutputs, 0)
	}
	// No need to finalize the progra since an empty program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}

// TestNewEmptyProgramLowBudget runs a program without instructions with
// insufficient funds.
func TestNewEmptyProgramLowBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	var r io.Reader
	// Execute the program.
	pt := newTestPriceTable()
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, types.ZeroCurrency, types.ZeroCurrency, newTestStorageObligation(true), 0, r)
	if !errors.Contains(err, modules.ErrMDMInsufficientBudget) {
		t.Fatal("missing error")
	}
	if err == nil {
		t.Fatal("ExecuteProgram should return an error")
	}
}

// TestNewProgramLowBudget runs a program with instructions with insufficient
// funds.
func TestNewProgramLowBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	// Create instruction.
	pt := newTestPriceTable()
	instructions, programData, _, _, collateral, _ := newReadSectorProgram(modules.SectorSize, 0, crypto.Hash{}, pt)
	r := bytes.NewReader(programData)
	dataLen := uint64(len(programData))
	// Execute the program with enough money to init the mdm but not enough
	// money to execute the first instruction.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, modules.MDMInitCost(pt, dataLen), collateral, newTestStorageObligation(true), dataLen, r)
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
	// Execute the program with no collateral budget.
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, types.ZeroCurrency, so, uint64(len(programData)), bytes.NewReader(programData))
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
