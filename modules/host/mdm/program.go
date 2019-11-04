package mdm

import (
	"context"
	"errors"
	"io"
	"sync"

	"gitlab.com/NebulousLabs/threadgroup"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

// programState contains some fields needed for the execution of instructions.
// The program's state is captured when the program is created and remains the
// same during the execution of the program.
type programState struct {
	blockHeight     types.BlockHeight
	secretKey       crypto.SecretKey
	settings        modules.HostExternalSettings
	currentRevision types.FileContractRevision
	host            Host
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
	staticFCID         types.FileContractID
	so                 StorageObligation
	instructions       []instruction
	staticData         *ProgramData
	staticProgramState *programState

	finalContractSize uint64 // contract size after executing all instructions

	staticNewValidProofValues  []types.SiacoinOutput
	staticNewMissedProofValues []types.SiacoinOutput

	executing  bool
	outputChan chan Output

	mu sync.Mutex
	tg *threadgroup.ThreadGroup
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func (mdm *MDM) NewProgram(fcid types.FileContractID, so StorageObligation, initialContractSize, programDataLen uint64, data io.Reader, newValidProofValues, newMissedProofValues []types.SiacoinOutput) *Program {
	// TODO: capture hostState
	return &Program{
		finalContractSize:          initialContractSize,
		outputChan:                 make(chan Output),
		staticProgramState:         nil,
		staticFCID:                 fcid,
		staticData:                 NewProgramData(data, programDataLen),
		staticNewValidProofValues:  newValidProofValues,
		staticNewMissedProofValues: newMissedProofValues,
		so:                         so,
		tg:                         &mdm.tg,
	}
}

// Execute will execute all of the program's instructions and create a file
// contract revision to be signed by the host and renter at the end. The ctx can
// be used to issue an interrupt which will stop the execution of the program as
// soon as the current instruction is done executing.
func (p *Program) Execute(ctx context.Context) (<-chan Output, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.executing {
		err := errors.New("can't call 'Execute' more than once")
		build.Critical(err)
		return nil, err
	}
	p.executing = true
	// Make sure that the contract is locked unless the program we're executing
	// is a readonly program.
	if !p.readOnly() && !p.so.Locked() {
		return nil, errors.New("contract needs to be locked for a program with one or more write instructions")
	}
	// Sanity check the new values for valid and missed proofs.
	if len(p.staticNewValidProofValues) != len(p.staticProgramState.currentRevision.NewValidProofOutputs) {
		return nil, errors.New("wrong number of valid proof values")
	} else if len(p.staticNewMissedProofValues) != len(p.staticProgramState.currentRevision.NewMissedProofOutputs) {
		return nil, errors.New("wrong number of missed proof values")
	}
	// Execute all the instructions.
	if err := p.tg.Add(); err != nil {
		return nil, err
	}
	go func() {
		defer p.tg.Done()
		defer close(p.outputChan)
		fcRoot := p.staticProgramState.currentRevision.NewFileMerkleRoot
		for _, i := range p.instructions {
			select {
			case <-ctx.Done(): // Check for interrupt
				break
			default:
			}
			// Execute next instruction.
			output := i.Execute(fcRoot)
			if output.Error != nil {
				// TODO: If the error was the host's fault refund the renter.
				break // Interrupt on execution error
			}
			fcRoot = output.NewMerkleRoot
			p.outputChan <- output
		}
	}()
	return p.outputChan, nil
}

// Result returns the new contract revision after the execution of the program.
// It should only be called after the channel returned by Execute is closed.
func (p *Program) Result() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.executing {
		err := errors.New("can't call 'Result' before 'Execute'")
		build.Critical(err)
		return err
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
