package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// EstimateProgramCosts estimates the execution cost, refund, collateral,
// memory, and time given a program in the form of a list of instructions.
func EstimateProgramCosts(pt modules.RPCPriceTable, instructions []modules.Instruction) (cost, refund, collateral types.Currency, memory, time uint64) {
	// TODO
	return
}

// costCalculator is a helper for calculating the costs of a program. It holds
// current running costs and the price table and allows updating them.
type costCalculator struct {
	pt                       modules.RPCPriceTable
	cost, refund, collateral types.Currency
	memory                   uint64
}

// update updates the running costs in the costCalculator and returns the latest
// costs.
func (c *costCalculator) update(newCost, newRefund, newCollateral types.Currency, newMemory, newTime uint64) (cost, refund, collateral types.Currency, memory uint64) {
	c.cost, c.refund, c.collateral, c.memory = updateRunningCosts(c.pt, c.cost, c.refund, c.collateral, c.memory, newCost, newRefund, newCollateral, newMemory, newTime)
	cost, refund, collateral, memory = c.cost, c.refund, c.collateral, c.memory
	return
}

// getCosts returns all stored costs.
func (c *costCalculator) getCosts() (cost, refund, collateral types.Currency, memory uint64) {
	cost, refund, collateral, memory = c.cost, c.refund, c.collateral, c.memory
	return
}

// updateRunningCosts is a helper testing function for updating the running
// costs of a program after adding an instruction.
func updateRunningCosts(pt modules.RPCPriceTable, runningCost, runningRefund, runningCollateral types.Currency, runningMemory uint64, newCost, newRefund, newCollateral types.Currency, newMemory, newTime uint64) (cost, refund, collateral types.Currency, memory uint64) {
	memory = runningMemory + newMemory
	memoryCost := modules.MDMMemoryCost(pt, memory, newTime)
	cost = runningCost.Add(memoryCost).Add(newCost)
	refund = runningRefund.Add(newRefund)
	collateral = runningCollateral.Add(newCollateral)
	return
}
