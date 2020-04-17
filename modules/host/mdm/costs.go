package mdm

import (
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// Costs contains all associated costs for an instruction, set of instructions
	// or program.
	Costs struct {
		ExecutionCost types.Currency
		Refund        types.Currency
		Collateral    types.Currency
		Memory        uint64
		Time          uint64
	}
)

// Equals returns true iff the two Costs objects are equal.
func (c Costs) Equals(c2 Costs) bool {
	return c.ExecutionCost.Cmp(c2.ExecutionCost) == 0 &&
		c.Refund.Cmp(c2.Refund) == 0 &&
		c.Collateral.Cmp(c2.Collateral) == 0 &&
		c.Memory == c2.Memory &&
		c.Time == c2.Time
}

// HumanString returns a human-readable representation of the Costs.
func (c *Costs) HumanString() string {
	return fmt.Sprintf("Costs { ExecutionCost: %v, Refund: %v, Collateral: %v, Memory: %v, Time: %v }", c.ExecutionCost.HumanString(), c.Refund.HumanString(), c.Collateral.HumanString(), c.Memory, c.Time)
}

// InitialProgramCosts returns the initial costs for a program with the given
// parameters.
func InitialProgramCosts(pt modules.RPCPriceTable, dataLen, numInstructions uint64) Costs {
	initExecutionCost := modules.MDMInitCost(pt, dataLen, numInstructions)
	initMemory := modules.MDMInitMemory()
	return Costs{
		ExecutionCost: initExecutionCost,
		Memory:        initMemory,
	}
}

// Update is a helper function for updating the running costs of a program after
// adding an instruction.
func (c Costs) Update(pt modules.RPCPriceTable, newCosts Costs) Costs {
	costs := Costs{}
	costs.Memory = c.Memory + newCosts.Memory
	memoryCost := modules.MDMMemoryCost(pt, costs.Memory, newCosts.Time)
	costs.ExecutionCost = c.ExecutionCost.Add(memoryCost).Add(newCosts.ExecutionCost)
	costs.Refund = c.Refund.Add(newCosts.Refund)
	costs.Collateral = c.Collateral.Add(newCosts.Collateral)
	costs.Time = c.Time + newCosts.Time
	return costs
}

// FinalizeProgramCosts finalizes the costs for a program by adding the cost of
// committing.
func (c Costs) FinalizeProgramCosts(pt modules.RPCPriceTable) Costs {
	c.ExecutionCost = c.ExecutionCost.Add(modules.MDMMemoryCost(pt, c.Memory, modules.MDMTimeCommit))
	return c
}

// EstimateProgramCosts estimates the execution cost, refund, collateral,
// memory, and time given a program in the form of a list of instructions.
func (instructions Instructions) EstimateProgramCosts(pt modules.RPCPriceTable, programDataLen uint64, data io.Reader) (Costs, error) {
	// Make a dummy program to allow us to get the instruction costs.
	p := &program{
		staticProgramState: &programState{
			priceTable: pt,
		},
		staticData: openProgramData(data, programDataLen),
	}
	runningCosts := InitialProgramCosts(pt, programDataLen, uint64(len(instructions)))

	for _, i := range instructions {
		// Decode instruction.
		instruction, err := decodeInstruction(p, i)
		if err != nil {
			return Costs{}, err
		}
		// Get the costs for the instruction.
		costs, err := instructionCosts(instruction)
		if err != nil {
			return Costs{}, err
		}
		// Update running costs.
		runningCosts = runningCosts.Update(pt, costs)
	}

	// Get the final costs for the program.
	finalCosts := runningCosts.FinalizeProgramCosts(pt)

	return finalCosts, nil
}
