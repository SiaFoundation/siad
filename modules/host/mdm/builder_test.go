package mdm

import (
	"bytes"
	"context"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// testBuilder is a helper used for constructing test programs.
type testBuilder struct {
	*modules.ProgramBuilder

	readonly      bool
	runningValues []values
	staticPT      *modules.RPCPriceTable
	values        values
}

// newTestBuilder creates a new testBuilder.
func newTestBuilder(pt *modules.RPCPriceTable, numInstructions, programDataLen uint64) *testBuilder {
	return &testBuilder{
		ProgramBuilder: modules.NewProgramBuilder(pt),

		readonly:      true,
		runningValues: make([]values, 0, numInstructions),
		staticPT:      pt,
		values: values{
			ExecutionCost: modules.MDMInitCost(pt, programDataLen, numInstructions),
			Memory:        modules.MDMInitMemory(),
		},
	}
}

// AssertOutputs finishes building the program, gets the costs, and executes the
// program. It asserts that the program has been built correctly and that the
// outputs of the program are as expected.
func (tb *testBuilder) AssertOutputs(mdm *MDM, so *TestStorageObligation, expectedOutputs []output) (func(so StorageObligation) error, *modules.RPCBudget, Output, error) {
	program, programData := tb.Program()
	dataLen := uint64(len(programData))
	values := tb.Cost(true)
	budget := modules.NewBudget(values.ExecutionCost)

	err := testCompareProgramValues(tb.staticPT, program, dataLen, bytes.NewReader(programData), values)
	if err != nil {
		return nil, nil, Output{}, err
	}

	finalizeFn, outputChan, err := mdm.ExecuteProgram(context.Background(), tb.staticPT, program, budget, values.Collateral, so, dataLen, bytes.NewReader(programData))
	if err != nil {
		return nil, nil, Output{}, err
	}
	if !tb.readonly && finalizeFn == nil {
		return nil, nil, Output{}, errors.New("could not retrieve finalizeFn function")
	}
	if tb.readonly && finalizeFn != nil {
		return nil, nil, Output{}, errors.New("finalizeFn callback should be nil for readonly program")
	}

	var i int
	var lastOutput Output
	for output := range outputChan {
		// Check outputs.
		values := tb.runningValues[i]
		expectedOutput := Output{
			output:               expectedOutputs[i],
			ExecutionCost:        values.ExecutionCost,
			AdditionalCollateral: values.Collateral,
			PotentialRefund:      values.Refund,
		}
		err := testCompareOutputs(output, expectedOutput)
		if err != nil {
			return nil, nil, Output{}, err
		}

		lastOutput = output
		i++
	}
	if i != len(expectedOutputs) {
		return nil, nil, Output{}, fmt.Errorf("expected number of outputs %v, got %v", i, len(expectedOutputs))
	}

	return finalizeFn, budget, lastOutput, nil
}

// Cost returns the final costs of the program.
func (tb *testBuilder) Cost(finalized bool) values {
	values := tb.values
	// Add the cost of finalizing the program.
	if finalized {
		values = values.Cost(tb.staticPT, tb.readonly)
	}
	return values
}

// TestAddAppendInstruction adds an append instruction to the builder, keeping
// track of running values.
func (tb *testBuilder) TestAddAppendInstruction(data []byte, merkleProof bool) {
	tb.AddAppendInstruction(data, merkleProof)

	collateral := modules.MDMAppendCollateral(tb.staticPT)
	cost, refund := modules.MDMAppendCost(tb.staticPT)
	memory := modules.MDMAppendMemory()
	time := uint64(modules.MDMTimeAppend)
	tb.addValues(collateral, cost, refund, memory, time, false)
}

// TestAddDropSectorsInstruction adds a dropsectors instruction to the builder,
// keeping track of running values.
func (tb *testBuilder) TestAddDropSectorsInstruction(numSectors uint64, merkleProof bool) {
	tb.AddDropSectorsInstruction(numSectors, merkleProof)

	collateral := modules.MDMDropSectorsCollateral()
	cost, refund := modules.MDMDropSectorsCost(tb.staticPT, numSectors)
	memory := modules.MDMDropSectorsMemory()
	time := modules.MDMDropSectorsTime(numSectors)
	tb.addValues(collateral, cost, refund, memory, time, false)
}

// TestAddHasSectorInstruction adds a hassector instruction to the builder, keeping track of running values.
func (tb *testBuilder) TestAddHasSectorInstruction(merkleRoot crypto.Hash) {
	tb.AddHasSectorInstruction(merkleRoot)

	collateral := modules.MDMHasSectorCollateral()
	cost, refund := modules.MDMHasSectorCost(tb.staticPT)
	memory := modules.MDMHasSectorMemory()
	time := uint64(modules.MDMTimeHasSector)
	tb.addValues(collateral, cost, refund, memory, time, true)
}

// TestAddReadSectorInstruction adds a readsector instruction to the builder,
// keeping track of running values.
func (tb *testBuilder) TestAddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	tb.AddReadSectorInstruction(length, offset, merkleRoot, merkleProof)

	collateral := modules.MDMReadCollateral()
	cost, refund := modules.MDMReadCost(tb.staticPT, length)
	memory := modules.MDMReadMemory()
	time := uint64(modules.MDMTimeReadSector)
	tb.addValues(collateral, cost, refund, memory, time, true)
}

// addValues updates the current running values for the program being built.
func (tb *testBuilder) addValues(collateral, cost, refund types.Currency, memory, time uint64, readonly bool) {
	values := values{cost, refund, collateral, memory}
	tb.values.AddValues(tb.staticPT, values, time)

	tb.runningValues = append(tb.runningValues, tb.values)

	if !readonly {
		tb.readonly = false
	}
}
