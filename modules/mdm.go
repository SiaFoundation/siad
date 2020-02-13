package modules

import (
	"encoding/binary"

	"gitlab.com/NebulousLabs/Sia/types"
)

type (
	// Instruction specifies a generic instruction used as an input to
	// `mdm.ExecuteProgram`.
	Instruction struct {
		Specifier InstructionSpecifier
		Args      []byte
	}
	// InstructionSpecifier specifies the type of the instruction.
	InstructionSpecifier types.Specifier
)

// MDM instruction cost component specifiers
var (
	MDMComponentCompute    = types.NewSpecifier("Compute")
	MDMComponentMemory     = types.NewSpecifier("Memory")
	MDMOperationDiskAccess = types.NewSpecifier("DiskAccess")
	MDMOperationDiskRead   = types.NewSpecifier("DiskRead")
	MDMOperationDiskWrite  = types.NewSpecifier("DiskWrite")
)

const (
	// RPCIReadSectorLen is the expected length of the 'Args' of a ReadSector
	// Instruction.
	RPCIReadSectorLen = 25
	// RPCIAppendLen is the expected length of the 'Args' of an Append
	// instructon.
	RPCIAppendLen = 9
)

var (
	// SpecifierAppend is the specifier for the Append RPC.
	SpecifierAppend = InstructionSpecifier{'A', 'p', 'p', 'e', 'n', 'd'}
	// SpecifierReadSector is the specifier for the ReadSector RPC.
	SpecifierReadSector = InstructionSpecifier{'R', 'e', 'a', 'd', 'S', 'e', 'c', 't', 'o', 'r'}
)

// RPCIReadSector is a convenience method to create an Instruction of type 'ReadSector'.
func RPCIReadSector(rootOff, offsetOff, lengthOff uint64, merkleProof bool) Instruction {
	args := make([]byte, RPCIReadSectorLen)
	binary.LittleEndian.PutUint64(args[:8], rootOff)
	binary.LittleEndian.PutUint64(args[8:16], offsetOff)
	binary.LittleEndian.PutUint64(args[16:24], lengthOff)
	if merkleProof {
		args[24] = 1
	}
	return Instruction{
		Args:      args,
		Specifier: SpecifierReadSector,
	}
}
