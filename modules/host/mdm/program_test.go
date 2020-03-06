package mdm

import (
	"context"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	var r io.Reader
	// Execute the program.
	pt := newTestPriceTable()
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, modules.MDMInitCost(pt, 0), newTestStorageObligation(true), 0, r)
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
	_, _, err := mdm.ExecuteProgram(context.Background(), pt, []modules.Instruction{}, types.ZeroCurrency, newTestStorageObligation(true), 0, r)
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
	var r io.Reader
	// Create instruction.
	pt := newTestPriceTable()
	instructions, r, dataLen, _, _, _ := newReadSectorProgram(modules.SectorSize, 0, crypto.Hash{}, pt)
	// Execute the program with enough money to init the mdm but not enough
	// money to execute the first instruction.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, modules.MDMInitCost(pt, dataLen), newTestStorageObligation(true), dataLen, r)
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
