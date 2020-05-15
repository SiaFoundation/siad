package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// values contains associated values for a test program.
	values struct {
		ExecutionCost types.Currency
		Refund        types.Currency
		Collateral    types.Currency
		Memory        uint64
	}
)

// Equals returns true iff the two values objects are equal.
func (v values) Equals(v2 values) bool {
	return v.ExecutionCost.Cmp(v2.ExecutionCost) == 0 &&
		v.Refund.Cmp(v2.Refund) == 0 &&
		v.Collateral.Cmp(v2.Collateral) == 0 &&
		v.Memory == v2.Memory
}

// HumanString returns a human-readable representation of the values.
func (v *values) HumanString() string {
	return fmt.Sprintf("values{ ExecutionCost: %v, Refund: %v, Collateral: %v, Memory: %v }", v.ExecutionCost.HumanString(), v.Refund.HumanString(), v.Collateral.HumanString(), v.Memory)
}

// AddValues is a testing helper function for updating the running values of a
// program after adding an instruction.
func (v *values) AddValues(pt *modules.RPCPriceTable, values values, time uint64) {
	v.Memory += values.Memory
	memoryCost := modules.MDMMemoryCost(pt, v.Memory, time)
	v.ExecutionCost = v.ExecutionCost.Add(memoryCost).Add(values.ExecutionCost)
	v.Refund = v.Refund.Add(values.Refund)
	v.Collateral = v.Collateral.Add(values.Collateral)
}

// Cost finalizes the values for a program by adding the cost of committing.
func (v values) Cost(pt *modules.RPCPriceTable, readonly bool) values {
	if !readonly {
		v.ExecutionCost = v.ExecutionCost.Add(modules.MDMMemoryCost(pt, v.Memory, modules.MDMTimeCommit))
	}
	return v
}
