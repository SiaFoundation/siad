package mdm

import (
	"io"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// EstimateProgramCosts estimates the execution cost, refund, collateral,
// memory, and time given a program in the form of a list of instructions.
func EstimateProgramCosts(pt modules.RPCPriceTable, instructions []modules.Instruction, programDataLen uint64, data io.Reader) (types.Currency, types.Currency, types.Currency, uint64, uint64, error) {
	// Make a dummy program to allow us to get the instruction costs.
	p := &Program{
		outputChan: nil,
		staticProgramState: &programState{
			priceTable: pt,
		},
		staticData: openProgramData(data, programDataLen),
	}
	initCost := modules.MDMInitCost(pt, programDataLen, uint64(len(instructions)))
	costCalculator := costCalculator{pt, initCost, types.ZeroCurrency, types.ZeroCurrency, modules.MDMInitMemory(), 0}

	for _, i := range instructions {
		// Decode instruction.
		instruction, err := decodeInstruction(p, i)
		if err != nil {
			return types.ZeroCurrency, types.ZeroCurrency, types.ZeroCurrency, 0, 0, err
		}
		// Get the costs for the instruction.
		cost, refund, collateral, memory, time, err := instructionCosts(instruction)
		if err != nil {
			return types.ZeroCurrency, types.ZeroCurrency, types.ZeroCurrency, 0, 0, err
		}
		// Update running costs.
		costCalculator.update(cost, refund, collateral, memory, time)
	}

	// Get the final costs for the program.
	cost, finalRefund, finalCollateral, finalMemory, finalTime := costCalculator.getCosts()
	finalCost := cost.Add(modules.MDMMemoryCost(pt, finalMemory, modules.MDMTimeCommit))

	return finalCost, finalRefund, finalCollateral, finalMemory, finalTime, nil
}

// costCalculator is a helper for calculating the costs of a program. It holds
// current running costs and the price table and allows updating them.
type costCalculator struct {
	pt                       modules.RPCPriceTable
	cost, refund, collateral types.Currency
	memory, time             uint64
}

// update updates the running costs in the costCalculator and returns the latest
// costs.
func (c *costCalculator) update(newCost, newRefund, newCollateral types.Currency, newMemory, newTime uint64) (cost, refund, collateral types.Currency, memory, time uint64) {
	c.cost, c.refund, c.collateral, c.memory, c.time = updateRunningCosts(c.pt, c.cost, c.refund, c.collateral, c.memory, c.time, newCost, newRefund, newCollateral, newMemory, newTime)
	cost, refund, collateral, memory, time = c.cost, c.refund, c.collateral, c.memory, c.time
	return
}

// getCosts returns all stored costs.
func (c *costCalculator) getCosts() (cost, refund, collateral types.Currency, memory, time uint64) {
	cost, refund, collateral, memory, time = c.cost, c.refund, c.collateral, c.memory, c.time
	return
}

// updateRunningCosts is a helper testing function for updating the running
// costs of a program after adding an instruction.
func updateRunningCosts(pt modules.RPCPriceTable, runningCost, runningRefund, runningCollateral types.Currency, runningMemory, runningTime uint64, newCost, newRefund, newCollateral types.Currency, newMemory, newTime uint64) (cost, refund, collateral types.Currency, memory, time uint64) {
	memory = runningMemory + newMemory
	memoryCost := modules.MDMMemoryCost(pt, memory, newTime)
	cost = runningCost.Add(memoryCost).Add(newCost)
	refund = runningRefund.Add(newRefund)
	collateral = runningCollateral.Add(newCollateral)
	time = runningTime + newTime
	return
}
