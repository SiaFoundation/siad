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

// ErrInsufficientBudget is returned if the program has to be aborted due to
// running out of resources.
var ErrInsufficientBudget = errors.New("program has insufficient budget to execute")

// programState contains some fields needed for the execution of instructions.
// The program's state is captured when the program is created and remains the
// same during the execution of the program.
type programState struct {
	// host related fields
	blockHeight types.BlockHeight
	host        Host
	// storage obligation related fields
	sectorsRemoved   []crypto.Hash
	sectorsGained    []crypto.Hash
	gainedSectorData [][]byte
}

// Program is a collection of instructions. Within a program, each instruction
// will potentially modify the size and merkle root of a file contract. After the
// final instruction is executed, the MDM will create an updated revision of the
// FileContract which has to be signed by the renter and the host.
type Program struct {
	// The contract ID specifies which contract is being modified by the MDM. If
	// all the instructions in the program are readonly instructions, the
	// program will execute in readonly mode which means that it will not lock
	// the contract before executing the instructions. This means that the
	// contract id field will be ignored.
	so                 StorageObligation
	instructions       []instruction
	staticData         *ProgramData
	staticProgramState *programState

	finalContractSize uint64      // contract size after executing all instructions
	initialMerkleRoot crypto.Hash // merkle root when program was created
	budget            Cost

	executing   bool
	finalOutput Output
	renterSig   types.TransactionSignature
	outputChan  chan Output

	mu sync.Mutex
	tg *threadgroup.ThreadGroup
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func (mdm *MDM) NewProgram(fcid types.FileContractID, so StorageObligation, initialContractSize uint64, initialMerkleRoot crypto.Hash, programDataLen uint64, data io.Reader) *Program {
	// TODO: capture hostState
	return &Program{
		finalContractSize:  initialContractSize,
		initialMerkleRoot:  initialMerkleRoot,
		outputChan:         make(chan Output),
		staticProgramState: nil,
		staticData:         NewProgramData(data, programDataLen),
		so:                 so,
		tg:                 &mdm.tg,
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
	// Make sure the budget covers the initial cost.
	budget, ok := p.budget.Min(InitCost(p.staticData.Len()))
	if !ok {
		return nil, ErrInsufficientBudget
	}
	// Execute all the instructions.
	if err := p.tg.Add(); err != nil {
		return nil, err
	}
	go func() {
		defer p.tg.Done()
		defer close(p.outputChan)
		p.mu.Lock()
		fcRoot := p.initialMerkleRoot
		p.mu.Unlock()
		for _, i := range p.instructions {
			select {
			case <-ctx.Done(): // Check for interrupt
				break
			default:
			}
			// Execute next instruction.
			output := i.Execute(fcRoot, budget)
			fcRoot = output.NewMerkleRoot
			p.outputChan <- output
			p.finalOutput = output
			// Abort if the last output contained an error.
			if output.Error != nil {
				break
			}
		}
	}()
	return p.outputChan, nil
}

// Finalize commits the changes made by the program to disk. It should only be
// called after the channel returned by Execute is closed.
func (p *Program) Finalize() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.executing {
		err := errors.New("can't call 'Result' before 'Execute'")
		build.Critical(err)
		return err
	}
	// Commit the changes to the storage obligation.
	ps := p.staticProgramState
	err := p.so.Update(ps.sectorsRemoved, ps.sectorsGained, ps.gainedSectorData)
	if err != nil {
		return err
	}
	return nil
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

// createRevisionSignature creates a signature for a file contract revision
// that signs on the file contract revision. The renter should have already
// provided the signature. createRevisionSignature will check to make sure that
// the renter's signature is valid.
func createRevisionSignature(fcr types.FileContractRevision, renterSig types.TransactionSignature, secretKey crypto.SecretKey, blockHeight types.BlockHeight) (types.Transaction, error) {
	hostSig := types.TransactionSignature{
		ParentID:       crypto.Hash(fcr.ParentID),
		PublicKeyIndex: 1,
		CoveredFields: types.CoveredFields{
			FileContractRevisions: []uint64{0},
		},
	}
	txn := types.Transaction{
		FileContractRevisions: []types.FileContractRevision{fcr},
		TransactionSignatures: []types.TransactionSignature{renterSig, hostSig},
	}
	sigHash := txn.SigHash(1, blockHeight)
	encodedSig := crypto.SignHash(sigHash, secretKey)
	txn.TransactionSignatures[1].Signature = encodedSig[:]
	err := modules.VerifyFileContractRevisionTransactionSignatures(fcr, txn.TransactionSignatures, blockHeight)
	if err != nil {
		return types.Transaction{}, err
	}
	return txn, nil
}
