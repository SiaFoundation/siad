package modules

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/types"
)

// InstructionValues contains all associated values for an instruction.
type InstructionValues struct {
	ExecutionCost types.Currency
	Refund        types.Currency
	Collateral    types.Currency
	Memory        uint64
	Time          uint64
	ReadOnly      bool
}

// ProgramValues contains associated values for a program.
type ProgramValues struct {
	ExecutionCost types.Currency
	Refund        types.Currency
	Collateral    types.Currency
	ReadOnly      bool
}

// RunningProgramValues contains all associated values for a running program.
type RunningProgramValues struct {
	ExecutionCost types.Currency
	Refund        types.Currency
	Collateral    types.Currency
	Memory        uint64
	ReadOnly      bool
}

// Equals returns true iff the two ProgramValues objects are equal.
func (v ProgramValues) Equals(v2 ProgramValues) bool {
	return v.ExecutionCost.Equals(v2.ExecutionCost) &&
		v.Refund.Equals(v2.Refund) &&
		v.Collateral.Equals(v2.Collateral) &&
		v.ReadOnly == v2.ReadOnly
}

// HumanString returns a human-readable representation of the ProgramValues.
func (v *ProgramValues) HumanString() string {
	return fmt.Sprintf("Values{ ExecutionCost: %v, Refund: %v, Collateral: %v, ReadOnly: %v }", v.ExecutionCost.HumanString(), v.Refund.HumanString(), v.Collateral.HumanString(), v.ReadOnly)
}

// Equals returns true iff the two RunningProgramValues objects are equal.
func (v RunningProgramValues) Equals(v2 RunningProgramValues) bool {
	return v.ExecutionCost.Cmp(v2.ExecutionCost) == 0 &&
		v.Refund.Cmp(v2.Refund) == 0 &&
		v.Collateral.Cmp(v2.Collateral) == 0 &&
		v.Memory == v2.Memory &&
		v.ReadOnly == v2.ReadOnly
}

// HumanString returns a human-readable representation of the
// RunningProgramValues.
func (v *RunningProgramValues) HumanString() string {
	return fmt.Sprintf("Values{ ExecutionCost: %v, Refund: %v, Collateral: %v, Memory: %v, ReadOnly: %v }", v.ExecutionCost.HumanString(), v.Refund.HumanString(), v.Collateral.HumanString(), v.Memory, v.ReadOnly)
}

// InitialProgramValues returns the initial values for a program with the given
// parameters.
func InitialProgramValues(pt *RPCPriceTable, dataLen, numInstructions uint64) RunningProgramValues {
	initExecutionCost := MDMInitCost(pt, dataLen, numInstructions)
	initMemory := MDMInitMemory()
	return RunningProgramValues{
		ExecutionCost: initExecutionCost,
		Memory:        initMemory,
		ReadOnly:      true,
	}
}

// AddValues is a helper function for updating the running values of a program
// after adding an instruction.
func (v *RunningProgramValues) AddValues(pt *RPCPriceTable, values InstructionValues) {
	v.Memory += values.Memory
	memoryCost := MDMMemoryCost(pt, v.Memory, values.Time)
	v.ExecutionCost = v.ExecutionCost.Add(memoryCost).Add(values.ExecutionCost)
	v.Refund = v.Refund.Add(values.Refund)
	v.Collateral = v.Collateral.Add(values.Collateral)
	if !values.ReadOnly {
		v.ReadOnly = false
	}
}

// FinalizeProgramValues finalizes the values for a program by adding the cost
// of committing.
func (v RunningProgramValues) FinalizeProgramValues(pt *RPCPriceTable, finalized bool) ProgramValues {
	cost := v.ExecutionCost
	if !v.ReadOnly && finalized {
		cost = cost.Add(MDMMemoryCost(pt, v.Memory, MDMTimeCommit))
	}
	values := ProgramValues{
		ExecutionCost: cost,
		Refund:        v.Refund,
		Collateral:    v.Collateral,
		ReadOnly:      v.ReadOnly,
	}
	return values
}
