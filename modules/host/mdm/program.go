package mdm

import (
	"context"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/threadgroup"

	"gitlab.com/NebulousLabs/Sia/modules"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// ErrInterrupted indicates that the program was interrupted during
	// execution and couldn't finish.
	ErrInterrupted = errors.New("execution of program was interrupted")
)

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
	merkleRoots      []crypto.Hash

	// statistic related fields
	potentialStorageRevenue types.Currency
	riskedCollateral        types.Currency
	potentialUploadRevenue  types.Currency

	// budget related fields
	priceTable modules.RPCPriceTable
}

// Program is a collection of instructions. Within a program, each instruction
// will potentially modify the size and merkle root of a file contract. After the
// final instruction is executed, the MDM will create an updated revision of the
// FileContract which has to be signed by the renter and the host.
type Program struct {
	so                 StorageObligation
	instructions       []instruction
	staticData         *programData
	staticProgramState *programState

	staticBudget    types.Currency
	executionCost   types.Currency
	potentialRefund types.Currency // refund if the program isn't committed

	renterSig  types.TransactionSignature
	outputChan chan Output

	tg *threadgroup.ThreadGroup
}

// outputFromError is a convenience function to wrap an error in an Output.
func outputFromError(err error, cost, refund types.Currency) Output {
	return Output{
		output: output{
			Error: err,
		},
		ExecutionCost:   cost,
		PotentialRefund: refund,
	}
}

// ExecuteProgram initializes a new program from a set of instructions and a reader
// which can be used to fetch the program's data and executes it.
func (mdm *MDM) ExecuteProgram(ctx context.Context, pt modules.RPCPriceTable, instructions []modules.Instruction, budget types.Currency, so StorageObligation, programDataLen uint64, data io.Reader) (func() error, <-chan Output, error) {
	p := &Program{
		outputChan: make(chan Output, len(instructions)),
		staticProgramState: &programState{
			blockHeight: mdm.host.BlockHeight(),
			host:        mdm.host,
			priceTable:  pt,
			merkleRoots: so.SectorRoots(),
		},
		staticBudget: budget,
		staticData:   openProgramData(data, programDataLen),
		so:           so,
		tg:           &mdm.tg,
	}

	// Convert the instructions.
	var err error
	var instruction instruction
	for _, i := range instructions {
		switch i.Specifier {
		case modules.SpecifierAppend:
			instruction, err = p.staticDecodeAppendInstruction(i)
		case modules.SpecifierReadSector:
			instruction, err = p.staticDecodeReadSectorInstruction(i)
		default:
			err = fmt.Errorf("unknown instruction specifier: %v", i.Specifier)
		}
		if err != nil {
			return nil, nil, errors.Compose(err, p.staticData.Close())
		}
		p.instructions = append(p.instructions, instruction)
	}
	// Make sure that the contract is locked unless the program we're executing
	// is a readonly program.
	if !p.readOnly() && !p.so.Locked() {
		err = errors.New("contract needs to be locked for a program with one or more write instructions")
		return nil, nil, errors.Compose(err, p.staticData.Close())
	}
	// Increment the execution cost of the program.
	err = p.addCost(InitCost(pt, p.staticData.Len()))
	if err != nil {
		return nil, nil, errors.Compose(err, p.staticData.Close())
	}
	// Execute all the instructions.
	if err := p.tg.Add(); err != nil {
		return nil, nil, errors.Compose(err, p.staticData.Close())
	}
	go func() {
		defer p.staticData.Close()
		defer p.tg.Done()
		defer close(p.outputChan)
		p.executeInstructions(ctx, so.ContractSize(), so.MerkleRoot())
	}()
	// If the program is readonly there is no need to finalize it.
	if p.readOnly() {
		return nil, p.outputChan, nil
	}
	return p.managedFinalize, p.outputChan, nil
}

// executeInstructions executes the programs instructions sequentially while
// returning the results to the caller using outputChan.
func (p *Program) executeInstructions(ctx context.Context, fcSize uint64, fcRoot crypto.Hash) {
	output := output{
		NewSize:       fcSize,
		NewMerkleRoot: fcRoot,
	}
	for _, i := range p.instructions {
		select {
		case <-ctx.Done(): // Check for interrupt
			p.outputChan <- outputFromError(ErrInterrupted, p.executionCost, p.potentialRefund)
			break
		default:
		}
		// Increment the cost before running the instruction.
		cost, refund, err := i.Cost()
		if err != nil {
			p.outputChan <- outputFromError(err, p.executionCost, p.potentialRefund)
			return
		}
		err = p.addCost(cost)
		if err != nil {
			p.outputChan <- outputFromError(err, p.executionCost, p.potentialRefund)
			return
		}
		// Add the instruction's potential refund to the total.
		p.potentialRefund = p.potentialRefund.Add(refund)
		// Execute next instruction.
		output = i.Execute(output)
		p.outputChan <- Output{
			output:          output,
			ExecutionCost:   p.executionCost,
			PotentialRefund: p.potentialRefund,
		}
		// Abort if the last output contained an error.
		if output.Error != nil {
			break
		}
	}
}

// managedFinalize commits the changes made by the program to disk. It should
// only be called after the channel returned by Execute is closed.
func (p *Program) managedFinalize() error {
	// Commit the changes to the storage obligation.
	ps := p.staticProgramState
	err := p.so.Update(ps.merkleRoots, ps.sectorsRemoved, ps.sectorsGained, ps.gainedSectorData)
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
