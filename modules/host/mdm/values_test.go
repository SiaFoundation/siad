package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

type (
	// TestValues contains associated values for a test program. It implements
	// the same functions as the MDMProgramBuilder for easier testing.
	TestValues struct {
		executionCost types.Currency
		refund        types.Currency
		collateral    types.Currency
		memory        uint64

		numInstructions   int
		programDataLength int

		readonly bool
		staticPT *modules.RPCPriceTable

		history []TestValues
	}
)

// NewTestValues creates a new instance of the TestValues with a given price
// table to compute the costs with.
func NewTestValues(pt *modules.RPCPriceTable) TestValues {
	return TestValues{
		readonly: true,
		staticPT: pt,
	}
}

// HumanString returns a human-readable representation of the TestValues.
func (v *TestValues) HumanString() string {
	return fmt.Sprintf("TestValues{ ExecutionCost: %v, Refund: %v, Collateral: %v, Memory: %v }",
		v.executionCost.HumanString(), v.refund.HumanString(), v.collateral.HumanString(), v.memory)
}

// AddAppendInstruction adds the cost of an append instruction to the object.
func (v *TestValues) AddAppendInstruction(data []byte) {
	memory := modules.MDMAppendMemory()
	collateral := modules.MDMAppendCollateral(v.staticPT)
	cost, refund := modules.MDMAppendCost(v.staticPT)
	time := uint64(modules.MDMTimeAppend)
	newData := len(data)
	readonly := false
	v.addInstruction(collateral, cost, refund, memory, time, newData, readonly)
}

// AddDropSectorsInstruction adds the cost of a drop sectors instruction to the
// object.
func (v *TestValues) AddDropSectorsInstruction(numSectors uint64) {
	collateral := modules.MDMDropSectorsCollateral()
	cost, refund := modules.MDMDropSectorsCost(v.staticPT, numSectors)
	memory := modules.MDMDropSectorsMemory()
	time := modules.MDMDropSectorsTime(numSectors)
	newData := 8
	readonly := false
	v.addInstruction(collateral, cost, refund, memory, time, newData, readonly)
}

// AddHasSectorInstruction adds a hassector instruction to the builder, keeping track of running values.
func (v *TestValues) AddHasSectorInstruction() {
	collateral := modules.MDMHasSectorCollateral()
	cost, refund := modules.MDMHasSectorCost(v.staticPT)
	memory := modules.MDMHasSectorMemory()
	time := uint64(modules.MDMTimeHasSector)
	newData := crypto.HashSize
	readonly := true
	v.addInstruction(collateral, cost, refund, memory, time, newData, readonly)
}

// AddReadSectorInstruction adds a readsector instruction to the builder,
// keeping track of running values.
func (v *TestValues) AddReadSectorInstruction(length uint64) {
	collateral := modules.MDMReadCollateral()
	cost, refund := modules.MDMReadCost(v.staticPT, length)
	memory := modules.MDMReadMemory()
	time := uint64(modules.MDMTimeReadSector)
	newData := 8 * 3
	readonly := true
	v.addInstruction(collateral, cost, refund, memory, time, newData, readonly)
}

// Cost returns the current cost of the program which would result . If
// 'finalized' is 'true', the memory cost of finalizing the program is included.
func (v TestValues) Cost() (cost, refund, collateral types.Currency) {
	// Calculate the init cost.
	cost = modules.MDMInitCost(v.staticPT, uint64(v.programDataLength), uint64(v.numInstructions))

	// Add the cost of the added instructions
	cost = cost.Add(v.executionCost)

	return cost, v.refund, v.collateral
}

// Budget is a convenience method which returns a budget that will exactly be
// enough for running the instructions previously added to the TestValues.
func (v TestValues) Budget(finalized bool) *modules.RPCBudget {
	cost, _, _ := v.Cost()
	// Add the cost of finalizing the program.
	if !v.readonly && finalized {
		cost = cost.Add(modules.MDMMemoryCost(v.staticPT, v.memory, modules.MDMTimeCommit))
	}
	return modules.NewBudget(cost)
}

// AssertOutputs finishes building the program, gets the costs, and executes the
// program. It asserts that the program has been built correctly and that the
// outputs of the program are as expected.
func (v *TestValues) AssertOutputs(outputs []Output) error {
	// Check the intermediarey values.
	var output Output
	for _, value := range append(v.history, *v) {
		// Get next output to compare.
		if len(outputs) == 0 {
			return errors.New("ran out of outputs")
		}
		output, outputs = outputs[0], outputs[1:]

		// Assert the output.
		err := value.AssertOutput(output)
		if err != nil {
			return err
		}
	}
	return nil
}

// AssertOutput compares the TestValues to the costs within the provided output.
func (v *TestValues) AssertOutput(output Output) error {
	cost, refund, collateral := v.Cost()
	if !output.ExecutionCost.Equals(cost) {
		return fmt.Errorf("execution costs don't match: %v != %v",
			cost.HumanString(), output.ExecutionCost.HumanString())
	}
	if !output.PotentialRefund.Equals(refund) {
		return fmt.Errorf("refund doesn't match: %v != %v",
			refund.HumanString(), output.PotentialRefund.HumanString())
	}
	if !output.AdditionalCollateral.Equals(collateral) {
		return fmt.Errorf("collateral doesn't match: %v != %v",
			collateral.HumanString(), output.AdditionalCollateral.HumanString())
	}
	return nil
}

// addInstruction adds the collateral, cost, refund and memory cost of an
// instruction to the value's state.
func (v *TestValues) addInstruction(collateral, cost, refund types.Currency, memory, time uint64, newData int, readonly bool) {
	// Backup the values before doing the modification.
	v.history = append(v.history, *v)
	// Update collateral
	v.collateral = v.collateral.Add(collateral)
	// Update memory and memory cost.
	v.memory += memory
	memoryCost := modules.MDMMemoryCost(v.staticPT, v.memory, time)
	v.executionCost = v.executionCost.Add(memoryCost)
	// Update execution cost and refund.
	v.executionCost = v.executionCost.Add(cost)
	v.refund = v.refund.Add(refund)
	// Update instructions, data and readonly states.
	v.numInstructions++
	v.programDataLength += newData
	v.readonly = v.readonly && readonly
}
