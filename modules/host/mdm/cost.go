package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// ErrInsufficientBudget is the error returned if the remaining budget of a
// program is not sufficient to execute the next instruction.
var ErrInsufficientBudget = errors.New("remaining budget is insufficient")

// programTime is the time it takes to execute a program. This is a
// hardcoded value which is meant to be replaced in the future.
// TODO: The time is hardcoded to 10 for now until we add time management in the
// future.
const programTime = 10

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
func InitCost(pt modules.RPCPriceTable, programLen uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(programLen).Mul64(programTime).Add(pt.InitBaseCost)
}

// HasSectorCost is the cost of executing a 'HasSector' instruction.
func HasSectorCost() Cost {
	return Cost{
		Compute:      1,
		DiskAccesses: 0,
		DiskRead:     0,
		DiskWrite:    0,
		Memory:       1 << 20, // 1 MiB
	}
}

// ReadCost is the cost of executing a 'Read' instruction. It is defined as:
// 'readBaseCost' + 'readLengthCost' * `readLength`
func ReadCost(pt modules.RPCPriceTable, readLength uint64) types.Currency {
	return pt.ReadLengthCost.Mul64(readLength).Add(pt.ReadBaseCost)
}

// WriteCost is the cost of executing a 'Write' instruction of a certain length.
// It's also used to compute the cost of a `WriteSector` and `Append`
// instruction.
func WriteCost(pt modules.RPCPriceTable, writeLength uint64) types.Currency {
	return pt.WriteLengthCost.Mul64(writeLength).Add(pt.WriteBaseCost)
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
