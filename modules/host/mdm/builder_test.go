package mdm

import (
	"fmt"

	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
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
	_, refund2, collateral2, _ := values.Cost()
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
func (tb *testProgramBuilder) AddAppendInstruction(data []byte, merkleProof bool, duration types.BlockHeight) {
	err := tb.staticPB.AddAppendInstruction(data, merkleProof, duration)
	if err != nil {
		panic(err)
	}
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

// AddSwapSectorInstruction adds a SwapSector instruction to the builder,
// keeping track of running values.
func (tb *testProgramBuilder) AddSwapSectorInstruction(sector1Idx, sector2Idx uint64, merkleProof bool) {
	tb.staticPB.AddSwapSectorInstruction(sector1Idx, sector2Idx, merkleProof)
	tb.staticValues.AddSwapSectorInstruction()
}

// AddUpdateRegistryInstruction adds an UpdateRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddUpdateRegistryInstruction(spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
	err := tb.staticPB.AddUpdateRegistryInstruction(spk, rv)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddUpdateRegistryInstruction(spk, rv)
}

// AddUpdateRegistryInstructionV156 adds an UpdateRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddUpdateRegistryInstructionV156(spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
	err := tb.staticPB.V156AddUpdateRegistryInstruction(spk, rv)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddUpdateRegistryInstruction(spk, rv)
}

// AddReadRegistryInstruction adds an ReadRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddReadRegistryInstruction(spk types.SiaPublicKey, tweak crypto.Hash, refunded bool, version modules.ReadRegistryVersion) types.Currency {
	refund, err := tb.staticPB.AddReadRegistryInstruction(spk, tweak, version)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddReadRegistryInstruction(spk, refunded)
	return refund
}

// AddReadRegistryInstructionV156 adds an ReadRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddReadRegistryInstructionV156(spk types.SiaPublicKey, tweak crypto.Hash, refunded bool) types.Currency {
	refund, err := tb.staticPB.V156AddReadRegistryInstruction(spk, tweak)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddReadRegistryInstruction(spk, refunded)
	return refund
}

// AddReadRegistryInstruction adds an ReadRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddReadRegistryEIDInstruction(sid modules.RegistryEntryID, refunded, needPubKeyAndTweak bool, version modules.ReadRegistryVersion) types.Currency {
	refund, err := tb.staticPB.AddReadRegistryEIDInstruction(sid, needPubKeyAndTweak, version)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddReadRegistryEIDInstruction(sid, refunded)
	return refund
}

// AddReadRegistryInstruction adds an ReadRegistry instruction to the
// builder, keeping track of running values.
func (tb *testProgramBuilder) AddReadRegistryEIDInstructionV156(sid modules.RegistryEntryID, refunded, needPubKeyAndTweak bool) types.Currency {
	refund, err := tb.staticPB.V156AddReadRegistryEIDInstruction(sid, needPubKeyAndTweak)
	if err != nil {
		panic(err)
	}
	tb.staticValues.AddReadRegistryEIDInstruction(sid, refunded)
	return refund
}

// Program returns the built program.
func (tb *testProgramBuilder) Program() (modules.Program, modules.ProgramData) {
	return tb.staticPB.Program()
}
