package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	host := newTestHost()
	mdm := New(host)
	// Shouldn't be able to execute empty program.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	budget := modules.NewBudget(modules.MDMInitCost(pt, 0, 0))
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, budget, types.ZeroCurrency, host.newTestStorageObligation(true), duration, 0, bytes.NewReader([]byte{}))
	if !errors.Contains(err, ErrEmptyProgram) {
		t.Fatal("expected ErrEmptyProgram", err)
	}
}

// TestNewProgramLowInitBudget runs a program that doesn't even have enough funds to init the MDM.
func TestNewProgramLowInitBudget(t *testing.T) {
	// Create MDM
	host := newTestHost()
	mdm := New(host)
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	pb := newTestProgramBuilder(pt, duration)
	pb.AddHasSectorInstruction(crypto.Hash{})
	program, data := pb.Program()
	// Execute the program.
	budget := modules.NewBudget(types.ZeroCurrency)
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, types.ZeroCurrency, host.newTestStorageObligation(true), duration, uint64(len(data)), bytes.NewReader(data))
	if !errors.Contains(err, modules.ErrMDMInsufficientBudget) {
		t.Fatal("missing error")
	}
}

// TestNewProgramLowBudget runs a program with instructions with insufficient
// funds.
func TestNewProgramLowBudget(t *testing.T) {
	// Create MDM
	host := newTestHost()
	mdm := New(host)
	// Create instruction.
	pt := newTestPriceTable()
	duration := types.BlockHeight(fastrand.Uint64n(5))
	pb := newTestProgramBuilder(pt, duration)
	pb.AddReadSectorInstruction(modules.SectorSize, 0, crypto.Hash{}, true)
	program, data := pb.Program()
	values := pb.Cost()
	_, _, collateral, _ := values.Cost()
	dataLen := uint64(len(data))
	// Execute the program with enough money to init the mdm but not enough
	// money to execute the first instruction.
	cost := modules.MDMInitCost(pt, dataLen, 1)
	budget := modules.NewBudget(cost)
	finalizeFn, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, collateral, host.newTestStorageObligation(true), duration, dataLen, bytes.NewReader(data))
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
	if finalizeFn != nil {
		t.Fatal("finalizeFn should be 'nil' for readonly programs")
	}
}

// TestNewProgramLowCollateralBudget runs a program with instructions with insufficient
// collateral budget.
func TestNewProgramLowCollateralBudget(t *testing.T) {
	host := newTestHost()
	// Create MDM
	mdm := New(host)
	// Create instruction.
	sectorData := fastrand.Bytes(int(modules.SectorSize))
	duration := types.BlockHeight(fastrand.Uint64n(5))
	pt := newTestPriceTable()
	pb := newTestProgramBuilder(pt, duration)
	pb.AddAppendInstruction(sectorData, false, duration)
	program, data := pb.Program()
	budget := pb.Cost().Budget(true)
	// Execute the program with no collateral budget.
	so := host.newTestStorageObligation(true)
	finalizeFn, outputs, err := mdm.ExecuteProgram(context.Background(), pt, program, budget, types.ZeroCurrency, so, duration, uint64(len(data)), bytes.NewReader(data))
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
			t.Fatalf("%v: using budget %v", err, budget)
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
	if err := finalizeFn(so); err == nil {
		t.Fatal("shouldn't be able to finalize program")
	}
}
