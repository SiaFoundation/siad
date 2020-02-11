package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// ErrInsufficientBudget is the error returned if the remaining budget of a
// program is not sufficient to execute the next instruction.
var ErrInsufficientBudget = errors.New("remaining budget is insufficient")

// subtractFromBudget will subtract an amount of money from a budget. In case of
// an underflow ErrInsufficientBudget and the unchanged budget are returned.
func subtractFromBudget(budget, toSub types.Currency) (types.Currency, error) {
	if toSub.Cmp(budget) > 0 {
		return budget, ErrInsufficientBudget
	}
	return budget.Sub(toSub), nil
}

// InitCost is the cost of instantiatine the MDM. It is defined as:
// 'InitBaseCost' + 'MemoryTimeCost' * 'programLen' * Time
// TODO: The time is hardcoded to 10 for now until we add time management in the
// future.
func InitCost(pt modules.RPCPriceTable, programLen uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(programLen).Mul64(10).Add(pt.InitBaseCost)
}

// ReadCost is the cost of executing a 'Read' instruction. It is defined as:
// 'readBaseCost' + 'readLengthCost' * `readLength`
func ReadCost(pt modules.RPCPriceTable, readLength uint64) types.Currency {
	return pt.ReadLengthCost.Mul64(readLength).Add(pt.ReadBaseCost)
}

// WriteSectorCost is the cost of executing a 'WriteSector' instruction.
func WriteSectorCost(pt modules.RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// CopyCost is the cost of executing a 'Copy' instruction.
func CopyCost(pt modules.RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// SwapCost is the cost of executing a 'Swap' instruction.
func SwapCost(pt modules.RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// TruncateCost is the cost of executing a 'Truncate' instruction.
func TruncateCost(pt modules.RPCPriceTable, contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}
