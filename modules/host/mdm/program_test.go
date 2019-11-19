package mdm

import (
	"context"
	"io"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
)

// TestNewEmptyProgram runs a program without instructions.
func TestNewEmptyProgram(t *testing.T) {
	// Create MDM
	mdm := New(newTestHost())
	var r io.Reader
	p := mdm.NewProgram(InitCost(0), newTestStorageObligation(true), 0, crypto.Hash{}, 0, r)
	// Execute the program.
	outputs, err := p.Execute(context.Background())
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
	// Finalize the program.
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
}
