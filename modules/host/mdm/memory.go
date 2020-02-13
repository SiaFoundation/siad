package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// AppendMemory returns the additional memory consumption of a 'Append' instruction.
func AppendMemory() uint64 {
	return modules.SectorSize // A full sector is added to the program's memory until the program is finalized.
}

// ReadMemory returns the additional memory consumption of a 'Read' instruction.
func ReadMemory() uint64 {
	return 0 // 'Read' doesn't hold on to any memory beyond the lifetime of the instruction.
}

// MemoryCost computes the memory cost given a price table, memory and time.
func MemoryCost(pt modules.RPCPriceTable, usedMemory, time uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(usedMemory * time)
}
