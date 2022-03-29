package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

type (
	// TestValues contains associated values for a test program. It implements
	// the same functions as the MDMProgramBuilder for easier testing.
	TestValues struct {
		batch         bool
		executionCost types.Currency
		failureRefund types.Currency
		collateral    types.Currency
		earlyRefund   types.Currency
		memory        uint64

		// These are pointers to share them between the whole history. That way,
		// when we add new values to the history, older values have their
		// program length and num instructions updated. That way we can easily
		// calculate the MDMInitCost of older values later.
		numInstructions   *int
		programDataLength *int

		readonly       bool
		staticDuration types.BlockHeight
		staticPT       *modules.RPCPriceTable

		history []TestValues
	}
)

// NewTestValues creates a new instance of the TestValues with a given price
// table to compute the costs with.
func NewTestValues(pt *modules.RPCPriceTable, duration types.BlockHeight) TestValues {
	return TestValues{
		readonly:          true,
		staticDuration:    duration,
		staticPT:          pt,
		memory:            modules.MDMInitMemory(),
		numInstructions:   new(int),
		programDataLength: new(int),
	}
}

// AddAppendInstruction adds the cost of an append instruction to the object.
func (v *TestValues) AddAppendInstruction(data []byte) {
	memory := modules.MDMAppendMemory()
	collateral := modules.MDMAppendCollateral(v.staticPT, v.staticDuration)
	cost, refund := modules.MDMAppendCost(v.staticPT, v.staticDuration)
	time := uint64(modules.MDMTimeAppend)
	newData := len(data)
	readonly := false
	batch := false
	v.addInstruction(collateral, cost, refund, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddDropSectorsInstruction adds the cost of a drop sectors instruction to the
// object.
func (v *TestValues) AddDropSectorsInstruction(numSectors uint64) {
	collateral := modules.MDMDropSectorsCollateral()
	cost := modules.MDMDropSectorsCost(v.staticPT, numSectors)
	memory := modules.MDMDropSectorsMemory()
	time := modules.MDMDropSectorsTime(numSectors)
	newData := 8
	readonly := false
	batch := false
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddHasSectorInstruction adds a hassector instruction to the builder, keeping
// track of running values.
func (v *TestValues) AddHasSectorInstruction() {
	collateral := modules.MDMHasSectorCollateral()
	cost := modules.MDMHasSectorCost(v.staticPT)
	memory := modules.MDMHasSectorMemory()
	time := uint64(modules.MDMTimeHasSector)
	newData := crypto.HashSize
	readonly := true
	batch := true
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddReadOffsetInstruction adds a readoffset instruction to the builder,
// keeping track of running values.
func (v *TestValues) AddReadOffsetInstruction(length uint64) {
	collateral := modules.MDMReadCollateral()
	cost := modules.MDMReadCost(v.staticPT, length)
	memory := modules.MDMReadMemory()
	time := uint64(modules.MDMTimeReadOffset)
	newData := 8 + 8
	readonly := true
	batch := false
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// keeping track of running values.
func (v *TestValues) AddReadSectorInstruction(length uint64) {
	collateral := modules.MDMReadCollateral()
	cost := modules.MDMReadCost(v.staticPT, length)
	memory := modules.MDMReadMemory()
	time := uint64(modules.MDMTimeReadSector)
	newData := 8 + 8 + crypto.HashSize
	readonly := true
	batch := false
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddRevisionInstruction adds a revision instruction to the builder, keeping
// track of running values.
func (v *TestValues) AddRevisionInstruction() {
	collateral := modules.MDMRevisionCollateral()
	cost := modules.MDMRevisionCost(v.staticPT)
	memory := modules.MDMRevisionMemory()
	time := uint64(modules.MDMTimeRevision)
	readonly := true
	batch := false
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, 0, readonly, batch)
}

// AddSwapSectorInstruction adds a revision instruction to the builder, keeping
// track of running values.
func (v *TestValues) AddSwapSectorInstruction() {
	collateral := modules.MDMSwapSectorCollateral()
	cost := modules.MDMSwapSectorCost(v.staticPT)
	memory := modules.MDMSwapSectorMemory()
	time := uint64(modules.MDMTimeSwapSector)
	newData := 8 + 8
	readonly := false
	batch := false
	v.addInstruction(collateral, cost, types.ZeroCurrency, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddUpdateRegistryInstruction adds a revision instruction to the builder, keeping
// track of running values.
func (v *TestValues) AddUpdateRegistryInstruction(spk types.SiaPublicKey, rv modules.SignedRegistryValue) {
	memory := modules.MDMUpdateRegistryMemory()
	collateral := modules.MDMUpdateRegistryCollateral()
	cost, refund := modules.MDMUpdateRegistryCost(v.staticPT)
	time := uint64(modules.MDMTimeUpdateRegistry)
	newData := crypto.HashSize + 8 + crypto.SignatureSize + len(rv.Data) + len(encoding.Marshal(spk))
	readonly := true
	batch := true
	v.addInstruction(collateral, cost, refund, types.ZeroCurrency, memory, time, newData, readonly, batch)
}

// AddReadRegistryInstruction adds a revision instruction to the builder, keeping
// track of running values.
func (v *TestValues) AddReadRegistryInstruction(spk types.SiaPublicKey, refunded bool) {
	memory := modules.MDMReadRegistryMemory()
	collateral := modules.MDMReadRegistryCollateral()
	cost, refund := modules.MDMReadRegistryCost(v.staticPT)
	time := uint64(modules.MDMTimeReadRegistry)
	newData := crypto.HashSize + len(encoding.Marshal(spk))
	readonly := true
	batch := true
	var successRefund types.Currency
	if refunded {
		successRefund = refund
	}
	v.addInstruction(collateral, cost, refund, successRefund, memory, time, newData, readonly, batch)
}

// AddReadRegistryEIDInstruction adds a revision instruction to the builder,
// keeping track of running values.
func (v *TestValues) AddReadRegistryEIDInstruction(sid modules.RegistryEntryID, refunded bool) {
	memory := modules.MDMReadRegistryMemory()
	collateral := modules.MDMReadRegistryCollateral()
	cost, refund := modules.MDMReadRegistryCost(v.staticPT)
	time := uint64(modules.MDMTimeReadRegistry)
	newData := len(encoding.Marshal(sid))
	readonly := true
	batch := true
	var successRefund types.Currency
	if refunded {
		successRefund = refund
	}
	v.addInstruction(collateral, cost, refund, successRefund, memory, time, newData, readonly, batch)
}

// Cost returns the current cost of the program which would result . If
// 'finalized' is 'true', the memory cost of finalizing the program is included.
func (v TestValues) Cost() (cost, failureRefund, collateral, instructionRefund types.Currency) {
	// Calculate the init cost.
	cost = modules.MDMInitCost(v.staticPT, uint64(*v.programDataLength), uint64(*v.numInstructions))

	// Add the cost of the added instructions
	cost = cost.Add(v.executionCost)

	return cost, v.failureRefund, v.collateral, v.earlyRefund
}

// Budget is a convenience method which returns a budget that will exactly be
// enough for running the instructions previously added to the TestValues.
func (v TestValues) Budget(finalized bool) *modules.RPCBudget {
	cost, _, _, _ := v.Cost()
	// Add the cost of finalizing the program.
	if !v.readonly && finalized {
		cost = cost.Add(modules.MDMMemoryCost(v.staticPT, v.memory, modules.MDMTimeCommit))
	}
	return modules.NewBudget(cost)
}

// AssertOutputs asserts a slice of MDM outputs against the TestValues' history.
func (v *TestValues) AssertOutputs(outputs []Output) error {
	// Check the whole history against the outputs.
	var output Output
	for i, value := range v.history {
		// Get next output to compare.
		if len(outputs) == 0 {
			return errors.New("ran out of outputs")
		}
		output, outputs = outputs[0], outputs[1:]

		// Determine whether we expect the instruction to be batched.
		batch := i < len(v.history)-1 && value.batch

		// Assert the output.
		err := value.AssertOutput(output, batch)
		if err != nil {
			return errors.AddContext(err, fmt.Sprintf("output #%v", i))
		}
	}
	// Check if there are any outputs left.
	if len(outputs) > 0 {
		return fmt.Errorf("expected 0 outputs left after assertion but got %v", len(outputs))
	}
	return nil
}

// AssertOutput compares the TestValues to the costs within the provided output.
func (v *TestValues) AssertOutput(output Output, batch bool) error {
	cost, refund, collateral, instructionRefund := v.Cost()
	if !output.ExecutionCost.Equals(cost.Sub(instructionRefund)) {
		return fmt.Errorf("execution costs don't match: %v != %v",
			cost.HumanString(), output.ExecutionCost.Sub(instructionRefund).HumanString())
	}
	if !output.FailureRefund.Equals(refund.Sub(instructionRefund)) {
		return fmt.Errorf("refund doesn't match: %v != %v",
			refund.HumanString(), output.FailureRefund.HumanString())
	}
	if !output.AdditionalCollateral.Equals(collateral) {
		return fmt.Errorf("collateral doesn't match: %v != %v",
			collateral.HumanString(), output.AdditionalCollateral.HumanString())
	}
	return nil
}

// addInstruction adds the collateral, cost, refund and memory cost of an
// instruction to the value's state.
func (v *TestValues) addInstruction(collateral, cost, failureRefund, successRefund types.Currency, memory, time uint64, newData int, readonly, batch bool) {
	// Update instruction refund.
	v.earlyRefund = v.earlyRefund.Add(successRefund)
	// Update collateral
	v.collateral = v.collateral.Add(collateral)
	// Update memory and memory cost.
	v.memory += memory
	memoryCost := modules.MDMMemoryCost(v.staticPT, v.memory, time)
	v.executionCost = v.executionCost.Add(memoryCost)
	// Update execution cost and refund.
	v.executionCost = v.executionCost.Add(cost)
	v.failureRefund = v.failureRefund.Add(failureRefund)
	// Update instructions, data and readonly states.
	*v.numInstructions++
	*v.programDataLength += newData
	v.readonly = v.readonly && readonly
	v.batch = batch
	// Add the new values to the history.
	v.history = append(v.history, *v)
}
