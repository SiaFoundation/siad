package mdm

import (
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// AppendMemory returns the additional memory consumption of a 'Append'
// instruction.
func AppendMemory() uint64 {
	// A full sector is added to the program's memory until the program is
	// finalized.
	return modules.SectorSize
}

// InitMemory returns the memory consumed by a program before considering the
// size of the program input.
func InitMemory() uint64 {
	return 1 << 20 // 1 MiB
}

// HasSectorMemory returns the additional memory consumption of a 'HasSector'
// instruction.
func HasSectorMemory() uint64 {
	// 'HasSector' doesn't hold on to any memory beyond the lifetime of the
	// instruction.
	return 0
}

// ReadMemory returns the additional memory consumption of a 'Read' instruction.
func ReadMemory() uint64 {
	// 'Read' doesn't hold on to any memory beyond the lifetime of the
	// instruction.
	return 0
}

// MemoryCost computes the memory cost given a price table, memory (in bytes)
// and time.
func MemoryCost(pt modules.RPCPriceTable, usedMemory, time uint64) types.Currency {
	return pt.MemoryTimeCost.Mul64(usedMemory * time)
}
