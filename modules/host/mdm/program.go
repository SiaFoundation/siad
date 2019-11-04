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
	// host related fields
	blockHeight types.BlockHeight
	secretKey   crypto.SecretKey
	settings    modules.HostExternalSettings
	host        Host
	// revision related fields
	currentRevision types.FileContractRevision
	// storage obligation related fields
	sectorsRemoved   []crypto.Hash
	sectorsGained    []crypto.Hash
	gainedSectorData [][]byte
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

	staticNewValidProofValues  []types.Currency
	staticNewMissedProofValues []types.Currency
	staticNewRevisionNumber    uint64

	executing   bool
	finalOutput Output
	renterSig   types.TransactionSignature
	outputChan  chan Output

	mu sync.Mutex
	tg *threadgroup.ThreadGroup
}

// NewProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data.
func (mdm *MDM) NewProgram(fcid types.FileContractID, so StorageObligation, initialContractSize, programDataLen uint64, data io.Reader, newValidProofValues, newMissedProofValues []types.Currency, NewRevisionNumber uint64) *Program {
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
			p.finalOutput = output
		}
	}()
	return p.outputChan, nil
}

// Result returns the signed txn with the new contract revision after the
// execution of the program. It should only be called after the channel returned
// by Execute is closed.
func (p *Program) Result() (types.Transaction, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.executing {
		err := errors.New("can't call 'Result' before 'Execute'")
		build.Critical(err)
		return types.Transaction{}, err
	}
	// Construct the new revision.
	currentRevision := p.staticProgramState.currentRevision
	newRevision := p.staticProgramState.currentRevision
	newRevision.NewRevisionNumber = p.staticNewRevisionNumber
	newRevision.NewFileSize = p.finalOutput.NewSize
	newRevision.NewFileMerkleRoot = p.finalOutput.NewMerkleRoot
	newRevision.NewValidProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewValidProofOutputs))
	for i := range newRevision.NewValidProofOutputs {
		newRevision.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      p.staticNewValidProofValues[i],
			UnlockHash: currentRevision.NewValidProofOutputs[i].UnlockHash,
		}
	}
	newRevision.NewMissedProofOutputs = make([]types.SiacoinOutput, len(currentRevision.NewMissedProofOutputs))
	for i := range newRevision.NewMissedProofOutputs {
		newRevision.NewMissedProofOutputs[i] = types.SiacoinOutput{
			Value:      p.staticNewMissedProofValues[i],
			UnlockHash: currentRevision.NewMissedProofOutputs[i].UnlockHash,
		}
	}
	// Sign the revision.
	txn, err := createRevisionSignature(newRevision, p.renterSig, p.staticProgramState.secretKey, p.staticProgramState.blockHeight)
	if err != nil {
		return types.Transaction{}, err
	}
	// If everything went well, commit the changes to the storage obligation.
	ps := p.staticProgramState
	err = p.so.Update(ps.sectorsRemoved, ps.sectorsGained, ps.gainedSectorData)
	if err != nil {
		return types.Transaction{}, err
	}
	return txn, nil
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
