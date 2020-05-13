package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// instructionValues contains all associated values for an instruction.
	instructionValues struct {
		ExecutionCost types.Currency
		Refund        types.Currency
		Collateral    types.Currency
		Memory        uint64
		Time          uint64
	}

	// programValues contains associated values for a program.
	programValues struct {
		ExecutionCost types.Currency
		Refund        types.Currency
		Collateral    types.Currency
	}

	// runningProgramValues contains all associated values for a running program.
	runningProgramValues struct {
		ExecutionCost types.Currency
		Refund        types.Currency
		Collateral    types.Currency
		Memory        uint64
	}
)

// Equals returns true iff the two programValues objects are equal.
func (v programValues) Equals(v2 programValues) bool {
	return v.ExecutionCost.Equals(v2.ExecutionCost) &&
		v.Refund.Equals(v2.Refund) &&
		v.Collateral.Equals(v2.Collateral)
}

// HumanString returns a human-readable representation of the programValues.
func (v *programValues) HumanString() string {
	return fmt.Sprintf("Values{ ExecutionCost: %v, Refund: %v, Collateral: %v }", v.ExecutionCost.HumanString(), v.Refund.HumanString(), v.Collateral.HumanString())
}

// Equals returns true iff the two runningProgramValues objects are equal.
func (v runningProgramValues) Equals(v2 runningProgramValues) bool {
	return v.ExecutionCost.Cmp(v2.ExecutionCost) == 0 &&
		v.Refund.Cmp(v2.Refund) == 0 &&
		v.Collateral.Cmp(v2.Collateral) == 0 &&
		v.Memory == v2.Memory
}

// HumanString returns a human-readable representation of the
// runningProgramValues.
func (v *runningProgramValues) HumanString() string {
	return fmt.Sprintf("Values{ ExecutionCost: %v, Refund: %v, Collateral: %v, Memory: %v }", v.ExecutionCost.HumanString(), v.Refund.HumanString(), v.Collateral.HumanString(), v.Memory)
}

// AddValues is a helper function for updating the running values of a program
// after adding an instruction.
func (v *runningProgramValues) AddValues(pt *modules.RPCPriceTable, values instructionValues) {
	v.Memory += values.Memory
	memoryCost := modules.MDMMemoryCost(pt, v.Memory, values.Time)
	v.ExecutionCost = v.ExecutionCost.Add(memoryCost).Add(values.ExecutionCost)
	v.Refund = v.Refund.Add(values.Refund)
	v.Collateral = v.Collateral.Add(values.Collateral)
}

// FinalizeProgramValues finalizes the values for a program by adding the cost
// of committing.
func (v runningProgramValues) FinalizeProgramValues(pt *modules.RPCPriceTable, readonly bool, finalized bool) programValues {
	cost := v.ExecutionCost
	if !readonly && finalized {
		cost = cost.Add(modules.MDMMemoryCost(pt, v.Memory, modules.MDMTimeCommit))
	}
	values := programValues{
		ExecutionCost: cost,
		Refund:        v.Refund,
		Collateral:    v.Collateral,
	}
	return values
}

// initialProgramValues returns the initial values for a program with the given
// parameters.
func initialProgramValues(pt *modules.RPCPriceTable, dataLen, numInstructions uint64) runningProgramValues {
	initExecutionCost := modules.MDMInitCost(pt, dataLen, numInstructions)
	initMemory := modules.MDMInitMemory()
	return runningProgramValues{
		ExecutionCost: initExecutionCost,
		Memory:        initMemory,
	}
}
