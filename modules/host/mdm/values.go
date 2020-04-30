package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
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
	return v.ExecutionCost.Cmp(v2.ExecutionCost) == 0 &&
		v.Refund.Cmp(v2.Refund) == 0 &&
		v.Collateral.Cmp(v2.Collateral) == 0 &&
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

// initialProgramValues returns the initial values for a program with the given
// parameters.
func initialProgramValues(pt *modules.RPCPriceTable, dataLen, numInstructions uint64) RunningProgramValues {
	initExecutionCost := modules.MDMInitCost(pt, dataLen, numInstructions)
	initMemory := modules.MDMInitMemory()
	return RunningProgramValues{
		ExecutionCost: initExecutionCost,
		Memory:        initMemory,
		ReadOnly:      true,
	}
}

// AddValues is a helper function for updating the running values of a program
// after adding an instruction.
func (v *RunningProgramValues) addValues(pt *modules.RPCPriceTable, values InstructionValues) {
	v.Memory += values.Memory
	memoryCost := modules.MDMMemoryCost(pt, v.Memory, values.Time)
	v.ExecutionCost = v.ExecutionCost.Add(memoryCost).Add(values.ExecutionCost)
	v.Refund = v.Refund.Add(values.Refund)
	v.Collateral = v.Collateral.Add(values.Collateral)
	if !values.ReadOnly {
		v.ReadOnly = false
	}
}

// finalizeProgramValues finalizes the values for a program by adding the cost
// of committing.
func (v RunningProgramValues) finalizeProgramValues(pt *modules.RPCPriceTable) ProgramValues {
	cost := v.ExecutionCost.Add(modules.MDMMemoryCost(pt, v.Memory, modules.MDMTimeCommit))
	values := ProgramValues{
		ExecutionCost: cost,
		Refund:        v.Refund,
		Collateral:    v.Collateral,
		ReadOnly:      v.ReadOnly,
	}
	return values
}

// testProgramValues estimates the execution cost, refund, collateral, memory,
// and time given a program in the form of a list of instructions. This function
// creates a dummy program that decodes the instructions and their parameters,
// testing that they were properly encoded.
func testProgramValues(p modules.Program, pt *modules.RPCPriceTable) (ProgramValues, error) {
	// Make a dummy program to allow us to get the instruction values.
	program := &program{
		staticProgramState: &programState{
			priceTable: pt,
		},
		staticData: openProgramData(p.Data, p.DataLen),
	}
	runningValues := initialProgramValues(pt, p.DataLen, uint64(len(p.Instructions)))

	for _, i := range p.Instructions {
		// Decode instruction.
		instruction, err := decodeInstruction(program, i)
		if err != nil {
			return ProgramValues{}, err
		}
		// Get the values for the instruction.
		values, err := instructionValues(instruction)
		if err != nil {
			return ProgramValues{}, err
		}
		// Update running values.
		runningValues.addValues(pt, values)
	}

	// Get the final values for the program.
	finalValues := runningValues.finalizeProgramValues(pt)

	return finalValues, nil
}
