package mdm

import (
	"context"
	"io"
	"sync"

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

	instructions []Instruction
	staticData   *ProgramData

	finalContractSize uint64 // contract size after executing all instructions

	mu sync.Mutex
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func NewProgram(fcid types.FileContractID, initialContractSize uint64, data io.Reader) *Program {
	return &Program{
		finalContractSize: initialContractSize,
		staticFCID:        fcid,
		staticData:        NewProgramData(data),
	}
}

// Execute will execute all of the program's instructions and create a file
// contract revision to be signed by the host and renter at the end. The ctx can
// be used to issue an interrupt which will stop the execution of the program as
// soon as the current instruction is done executing.
func (p *Program) Execute(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.readOnly() {
		// TODO: Lock contract
		panic("not implemented yet")
	}
	// Execute all the instructions.
	for _, i := range p.instructions {
		i.Execute()
	}
	panic("not implemented yet")
}

// managedCost returns the amount of money that the execution of the program
// costs. It is the cost of all of the instructions.
func (p *Program) managedCost() (cost Cost) {
	p.mu.Lock()
	defer p.mu.Lock()
	// TODO: This is actually not quite true. We fetch the program's data in the
	// background so we don't know how much data is transmitted in total.
	for _, i := range p.instructions {
		cost = cost.Add(i.Cost())
	}
	return
}

// readOnly returns 'true' if all of the instructions executed by a program are
// readonly.
func (p *Program) readOnly() bool {
	for _, i := range p.instructions {
		if !i.ReadOnly() {
			return false
		}
	}
	return true
}
