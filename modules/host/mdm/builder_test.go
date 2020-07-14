package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// testProgramBuilder is a helper used for constructing test programs and
// implicitly testing the modules.MDMProgramBuilder.
type testProgramBuilder struct {
	readonly bool
	staticPT *modules.RPCPriceTable

	// staticPB is an instance of the production program builder.
	staticPB *modules.ProgramBuilder

	// staticValues are the test implementation of an accumulator which the
	// production program builder will implicitly be tested against.
	staticValues TestValues
}

// newTestProgramBuilder creates a new testBuilder.
func newTestProgramBuilder(pt *modules.RPCPriceTable, duration types.BlockHeight) *testProgramBuilder {
	return &testProgramBuilder{
		readonly: true,
		staticPT: pt,

		staticPB:     modules.NewProgramBuilder(pt, duration),
		staticValues: NewTestValues(pt, duration),
	}
}

// assertCosts makes sure that both values and pb return the same cost for
// complete programs.
func assertCosts(finalized bool, values TestValues, pb *modules.ProgramBuilder) {
	cost1, refund1, collateral1 := pb.Cost(finalized)
	_, refund2, collateral2 := values.Cost()
	cost2 := values.Budget(finalized).Remaining()
	if !cost1.Equals(cost2) {
		panic(fmt.Sprintf("cost: %v != %v", cost1.HumanString(), cost2.HumanString()))
	}
	if !refund1.Equals(refund2) {
		panic(fmt.Sprintf("refund: %v != %v", refund1.HumanString(), refund2.HumanString()))
	}
	if !collateral1.Equals(collateral2) {
		panic(fmt.Sprintf("collateral: %v != %v", collateral1.HumanString(), collateral2.HumanString()))
	}
}

// Cost returns the final costs of the program.
func (tb *testProgramBuilder) Cost() TestValues {
	// Make sure the programBuilder and test values produce the same costs.
	assertCosts(true, tb.staticValues, tb.staticPB)
	assertCosts(false, tb.staticValues, tb.staticPB)
	// Return the test values for convenience.
	return tb.staticValues
}

// AddAppendInstruction adds an append instruction to the builder, keeping
// track of running values.
func (tb *testProgramBuilder) AddAppendInstruction(data []byte, merkleProof bool) {
	tb.staticPB.AddAppendInstruction(data, merkleProof)
	tb.staticValues.AddAppendInstruction(data)
}

// AddDropSectorsInstruction adds a dropsectors instruction to the builder,
// keeping track of running values.
func (tb *testProgramBuilder) AddDropSectorsInstruction(numSectors uint64, merkleProof bool) {
	tb.staticPB.AddDropSectorsInstruction(numSectors, merkleProof)
	tb.staticValues.AddDropSectorsInstruction(numSectors)
}

// AddHasSectorInstruction adds a hassector instruction to the builder, keeping track of running values.
func (tb *testProgramBuilder) AddHasSectorInstruction(merkleRoot crypto.Hash) {
	tb.staticPB.AddHasSectorInstruction(merkleRoot)
	tb.staticValues.AddHasSectorInstruction()
}

// AddReadOffsetInstruction adds a readoffset instruction to the builder,
// keeping track of running values.
func (tb *testProgramBuilder) AddReadOffsetInstruction(length, offset uint64, merkleProof bool) {
	tb.staticPB.AddReadOffsetInstruction(length, offset, merkleProof)
	tb.staticValues.AddReadOffsetInstruction(length)
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// keeping track of running values.
func (tb *testProgramBuilder) AddReadSectorInstruction(length, offset uint64, merkleRoot crypto.Hash, merkleProof bool) {
	tb.staticPB.AddReadSectorInstruction(length, offset, merkleRoot, merkleProof)
	tb.staticValues.AddReadSectorInstruction(length)
}

// AddRevisionInstruction adds a revision instruction to the builder, keeping
// track of running values.
func (tb *testProgramBuilder) AddRevisionInstruction() {
	tb.staticPB.AddRevisionInstruction()
	tb.staticValues.AddRevisionInstruction()
}

// Program returns the built program.
func (tb *testProgramBuilder) Program() (modules.Program, modules.ProgramData) {
	return tb.staticPB.Program()
}
