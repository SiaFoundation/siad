package mdm

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// testBuilder is a helper used for constructing test programs.
type testBuilder struct {
	*modules.ProgramBuilder
	staticInitCost types.Currency
}

// newTestBuilder creates a new testBuilder.
func newTestBuilder(pt *modules.RPCPriceTable, numInstructions, programDataLen uint64) *testBuilder {
	builder := modules.NewProgramBuilder(pt)
	tb := &testBuilder{
		builder,
		modules.MDMInitCost(pt, programDataLen, numInstructions),
	}
	return tb
}

// TestAddAppendInstruction adds an append instruction to the builder, keeping
// track of running values.
func (tb *testBuilder) TestAddAppendInstruction(data []byte, merkleProof bool) runningProgramValues {
	tb.AddAppendInstruction(data, merkleProof)
	return tb.runningValues()
}

// TestAddDropSectorsInstruction adds a dropsectors instruction to the builder,
// keeping track of running values.
func (tb *testBuilder) TestAddDropSectorsInstruction(numSectors uint64, merkleProof bool) runningProgramValues {
	tb.AddDropSectorsInstruction(numSectors, merkleProof)
	return tb.runningValues()
}

// TestAddHasSectorInstruction adds a hassector instruction to the builder, keeping track of running values.
func (tb *testBuilder) TestAddHasSectorInstruction(merkleRoot crypto.Hash) runningProgramValues {
	tb.AddHasSectorInstruction(merkleRoot)
	return tb.runningValues()
}

// TestAddReadSectorInstruction adds a readsector instruction to the builder,
// keeping track of running values.
func (tb *testBuilder) TestAddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) runningProgramValues {
	tb.AddReadSectorInstruction(length, offset, merkleRoot, merkleProof)
	return tb.runningValues()
}

// Values returns the final values of the program as programValues.
func (tb *testBuilder) Values() programValues {
	cost, refund, collateral := tb.Cost(true)
	return programValues{cost, refund, collateral}
}

// runningValues returns the current running values for the program being built.
func (tb *testBuilder) runningValues() runningProgramValues {
	cost := tb.staticInitCost
	cost = cost.Add(tb.ExecutionCost)
	return runningProgramValues{cost, tb.PotentialRefund, tb.RiskedCollateral, tb.UsedMemory}
}
