package mdm

import (
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

// InitCost is the cost of instantiating the MDM
func InitCost(programLen uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// ReadCost is the cost of executing a 'Read' instruction. It is defined as:
// 'readBaseCost' + 'readLengthCost' * `readLength`
func ReadCost(readBaseCost, readLengthCost types.Currency, readLength uint64) types.Currency {
	return readLengthCost.Mul64(readLength).Add(readBaseCost)
}

// WriteSectorCost is the cost of executing a 'WriteSector' instruction.
func WriteSectorCost(contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// CopyCost is the cost of executing a 'Copy' instruction.
func CopyCost(contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// SwapCost is the cost of executing a 'Swap' instruction.
func SwapCost(contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}

// TruncateCost is the cost of executing a 'Truncate' instruction.
func TruncateCost(contractSize uint64) types.Currency {
	return types.SiacoinPrecision // TODO: figure out good cost
}
