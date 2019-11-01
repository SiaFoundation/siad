package mdm

import (
	"context"
	"errors"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/modules/host/mdm/storageobligation"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// hostState captures some fields
type hostState struct {
	blockHeight     types.BlockHeight
	secretKey       crypto.SecretKey
	settings        modules.HostExternalSettings
	currentRevision types.FileContractRevision
}

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
	staticFCID      types.FileContractID
	so              storageobligation.StorageObligation
	instructions    []Instruction
	staticData      *ProgramData
	staticHostState *hostState

	finalContractSize uint64 // contract size after executing all instructions

	mu sync.Mutex
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func (mdm *MDM) NewProgram(fcid types.FileContractID, so StorageObligation, initialContractSize uint64, data io.Reader, newValidProofValues, newMissedProofValues types.Currency) *Program {
	// TODO: capture hostState
	return &Program{
		finalContractSize: initialContractSize,
		staticHostState:   nil,
		staticFCID:        fcid,
		staticData:        NewProgramData(data),
		so:                so,
	}
}

// Execute will execute all of the program's instructions and create a file
// contract revision to be signed by the host and renter at the end. The ctx can
// be used to issue an interrupt which will stop the execution of the program as
// soon as the current instruction is done executing.
func (p *Program) Execute(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	// Make sure that the contract is locked unless the program we're executing
	// is a readonly program.
	if !p.readOnly() && len(s.so.OriginTransactionSet) == 0 {
		return errors.New("contract needs to be locked for a program with one or more write instructions")
	}
	// Sanity check the new values for valid and missed proofs.
	if len(p.NewValidProofValues) != len(p.staticHostState.currentRevision.NewValidProofOutputs) {
		return errors.New("wrong number of valid proof values")
	} else if len(p.NewMissedProofValues) != len(p.staticHostState.currentRevision.NewMissedProofOutputs) {
		return errors.New("wrong number of missed proof values")
	}
	// Execute all the instructions.
	for _, i := range p.instructions {
		i.Execute()
	}
	// TODO: Update the storage obligation.
	// TODO: Construct the new revision.
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
