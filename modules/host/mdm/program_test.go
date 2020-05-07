package mdm

import (
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
)

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	// Shouldn't be able to execute empty program.
	pt := newTestPriceTable()
	budget := modules.NewBudget(modules.MDMInitCost(pt, 0, 0))
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, modules.NewProgram(), budget, types.ZeroCurrency, newTestStorageObligation(true))
	if !errors.Contains(err, ErrEmptyProgram) {
		t.Fatal("expected ErrEmptyProgram", err)
	}
}

// TestNewProgramLowInitBudget runs a program that doesn't even have enough funds to init the MDM.
func TestNewProgramLowInitBudget(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	pt := newTestPriceTable()
	pb := modules.NewProgramBuilder()
	pb.AddHasSectorInstruction(crypto.Hash{})
	program := pb.Program()
	// Execute the program.
	budget := modules.NewBudget(types.ZeroCurrency)
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, types.ZeroCurrency, newTestStorageObligation(true))
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
	pb := modules.NewProgramBuilder()
	pb.AddReadSectorInstruction(modules.SectorSize, 0, crypto.Hash{}, true)
	program := pb.Program()
	_, finalValues := pb.Values(pt, true)
	// Execute the program with enough money to init the mdm but not enough
	// money to execute the first instruction.
	cost := modules.MDMInitCost(pt, program.DataLen, 1)
	budget := modules.NewBudget(cost)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, finalValues.Collateral, newTestStorageObligation(true))
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
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	pt := newTestPriceTable()
	pb := modules.NewProgramBuilder()
	err := pb.AddAppendInstruction(sectorData, false)
	if err != nil {
		t.Fatal(err)
	}
	program := pb.Program()
	_, values := pb.Values(pt, true)
	// Execute the program with no collateral budget.
	budget := modules.NewBudget(values.ExecutionCost)
	so := newTestStorageObligation(true)
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, types.ZeroCurrency, so)
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
			t.Fatalf("%v: using budget %v", err, values.HumanString())
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
