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

// addCost increases the cost of the program by 'cost'. If as a result the cost
// becomes larger than the budget of the program, ErrInsufficientBudget is
// returned.
func (p *Program) addCost(cost types.Currency) error {
	newExecutionCost := p.executionCost.Add(cost)
	if p.staticBudget.Cmp(newExecutionCost) < 0 {
		return ErrInsufficientBudget
	}
	p.executionCost = newExecutionCost
	return nil
}

// AppendCost is the cost of executing an 'Append' instruction.
func AppendCost(pt modules.RPCPriceTable) (types.Currency, types.Currency) {
	cost := WriteCost(pt, modules.SectorSize)
	refund := types.ZeroCurrency // TODO: figure out good refund
	return cost, refund
}

// InitCost is the cost of instantiatine the MDM. It is defined as:
// 'InitBaseCost' + 'MemoryTimeCost' * 'programLen' * Time
func InitCost(pt modules.RPCPriceTable, programLen uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(programLen).Mul64(programTime).Add(pt.InitBaseCost)
}

// ReadCost is the cost of executing a 'Read' instruction. It is defined as:
// 'readBaseCost' + 'readLengthCost' * `readLength`
func ReadCost(pt modules.RPCPriceTable, readLength uint64) types.Currency {
	return pt.ReadLengthCost.Mul64(readLength).Add(pt.ReadBaseCost)
}

// WriteCost is the cost of executing a 'Write' instruction of a certain length.
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
