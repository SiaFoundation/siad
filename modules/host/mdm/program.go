package mdm

import (
	"context"
	"io"

	"gitlab.com/NebulousLabs/Sia/types"
)

// Program is a collection of instructions. Within a program, each instruction
// will potentially modify the size and merkle root of a file contract. Afte the
// final instruction is executed, the MDM will create an updated revision of the
// FileContract which has to be signed by the renter and the host.
type Program struct {
	// The contract specifies which contract is being modified by the MDM. If
	// all the instructions in the program are readonly instructions, the
	// program will execute in readonly mode which means that it will not lock
	// the contract before executing the instructions. This means that the
	// contract id field will be ignored.
	staticFCID types.FileContractID

	staticInstructions []instruction.Instruction
	staticData         ProgramData
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func NewProgram(fcid types.FileContractID, instructions []Instruction, data io.Reader) *Program {
	return &Program{
		staticFCID:         fcid,
		staticData:         NewProgramData(data),
		staticInstructions: instructions,
	}
}

// Execute will execute all of the program's instructions and create a file
// contract revision to be signed by the host and renter at the end. The ctx can
// be used to issue an interrupt which will stop the execution of the program as
// soon as the current instruction is done executing.
func (p *Program) Execute(ctx context.Context) error {
	if !p.managedReadonly() {
		// TODO: Lock contract
	}
	panic("not implemented yet")
}

// readOnly returns 'true' if all of the instructions executed by a program are
// readonly.
func (p *Program) managedReadOnly() bool {
	for _, i := range staticInstructions {
		if !i.ReadOnly() {
			return false
		}
	}
	return true
}
