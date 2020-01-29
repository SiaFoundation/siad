package mdm

import (
	"context"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/errors"
)

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	var r io.Reader
	// Execute the program.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), []modules.Instruction{}, InitCost(0), newTestStorageObligation(true), 0, crypto.Hash{}, 0, r)
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
	_, _, err := mdm.ExecuteProgram(context.Background(), []modules.Instruction{}, Cost{}, newTestStorageObligation(true), 0, crypto.Hash{}, 0, r)
	if !errors.Contains(err, ErrInsufficientBudget) {
		t.Fatal("missing error")
	}
	if !errors.Contains(err, ErrInsufficientMemoryBudget) {
		t.Fatal("missing error")
	}
	if !errors.Contains(err, ErrInsufficientDiskAccessesBudget) {
		t.Fatal("missing error")
	}
	if !errors.Contains(err, ErrInsufficientComputeBudget) {
		t.Fatal("missing error")
	}
	if errors.Contains(err, ErrInsufficientDiskReadBudget) {
		t.Fatal("wrong error")
	}
	if errors.Contains(err, ErrInsufficientDiskWriteBudget) {
		t.Fatal("wrong error")
	}
	if err == nil {
		t.Fatal("ExecuteProgram should return an error")
	}
}
