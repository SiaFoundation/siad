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
