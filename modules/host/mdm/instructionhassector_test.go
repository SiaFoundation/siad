package mdm

import (
	"bytes"
	"context"
	"testing"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// newHasSectorInstruction is a convenience method for creating a single
// 'HasSector' instruction.
func newHasSectorInstruction(dataOffset uint64, pt modules.RPCPriceTable) (modules.Instruction, types.Currency, types.Currency, types.Currency, uint64, uint64) {
	i := NewHasSectorInstruction(dataOffset)
	cost, refund := modules.MDMHasSectorCost(pt)
	collateral := modules.MDMHasSectorCollateral()
	return i, cost, refund, collateral, modules.MDMHasSectorMemory(), modules.MDMTimeHasSector
}

// newHasSectorProgram is a convenience method which prepares the instructions
// and the program data for a program that executes a single
// HasSectorInstruction.
func newHasSectorProgram(merkleRoot crypto.Hash, pt modules.RPCPriceTable) ([]modules.Instruction, []byte, types.Currency, types.Currency, types.Currency, uint64) {
	data := make([]byte, crypto.HashSize)
	copy(data[:crypto.HashSize], merkleRoot[:])
	initCost := modules.MDMInitCost(pt, uint64(len(data)), 1)
	i, cost, refund, collateral, memory, time := newHasSectorInstruction(0, pt)
	cost, refund, collateral, memory = updateRunningCosts(pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), cost, refund, collateral, memory, time)
	instructions := []modules.Instruction{i}
	cost = cost.Add(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit))
	return instructions, data, cost, refund, collateral, memory
}

// TestInstructionHasSector tests executing a program with a single
// HasSectorInstruction.
func TestInstructionHasSector(t *testing.T) {
	host := newTestHost()
	mdm := New(host)
	defer mdm.Stop()

	// Add a random sector to the host.
	sectorRoot := randomSector()
	_, err := host.ReadSector(sectorRoot)
	if err != nil {
		t.Fatal(err)
	}
	// Create a program to check for a sector on the host.
	so := newTestStorageObligation(true)
	so.sectorRoots = randomSectorRoots(1)
	sectorRoot = so.sectorRoots[0]
	pt := newTestPriceTable()
	instructions, programData, cost, refund, collateral, memory := newHasSectorProgram(sectorRoot, pt)
	dataLen := uint64(len(programData))
	ics := so.ContractSize()
	imr := so.MerkleRoot()

	// Expected outputs.
	expectedOutputs := []Output{
		{
			output{
				NewSize:       ics,
				NewMerkleRoot: imr,
				Proof:         []crypto.Hash{},
				Output:        []byte{1},
			},
			cost.Sub(modules.MDMMemoryCost(pt, memory, modules.MDMTimeCommit)),
			collateral,
			refund,
		},
	}

	// Execute it.
	finalize, outputs, err := mdm.ExecuteProgram(context.Background(), pt, instructions, cost, collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		t.Fatal(err)
	}

	// Check outputs.
	_, err = testCompareOutputs(outputs, expectedOutputs)
	if err != nil {
		t.Fatal(err)
	}

	// No need to finalize the program since this program is readonly.
	if finalize != nil {
		t.Fatal("finalize callback should be nil for readonly program")
	}
}
