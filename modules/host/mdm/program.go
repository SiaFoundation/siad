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

	// program cache
	sectors sectors

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
	instructions       []instruction
	staticData         *programData
	staticProgramState *programState

	staticBudget           types.Currency
	staticCollateralBudget types.Currency
	executionCost          types.Currency
	newCollateral          types.Currency // collateral the host is required to add
	potentialRefund        types.Currency // refund if the program isn't committed
	usedMemory             uint64

	renterSig  types.TransactionSignature
	outputChan chan Output
	outputErr  error // contains the error of the first instruction of the program that failed

	tg *threadgroup.ThreadGroup
}

// outputFromError is a convenience function to wrap an error in an Output.
func outputFromError(err error, collateral, cost, refund types.Currency) Output {
	return Output{
		output: output{
			Error: err,
		},
		ExecutionCost:   cost,
		NewCollateral:   collateral,
		PotentialRefund: refund,
	}
}

// decodeInstruction creates a specific instance of an instruction from a
// specified generic instruction.
func decodeInstruction(p *Program, i modules.Instruction) (instruction, error) {
	switch i.Specifier {
	case modules.SpecifierAppend:
		return p.staticDecodeAppendInstruction(i)
	case modules.SpecifierDropSectors:
		return p.staticDecodeDropSectorsInstruction(i)
	case modules.SpecifierHasSector:
		return p.staticDecodeHasSectorInstruction(i)
	case modules.SpecifierReadSector:
		return p.staticDecodeReadSectorInstruction(i)
	default:
		return nil, fmt.Errorf("unknown instruction specifier: %v", i.Specifier)
	}
}

// ExecuteProgram initializes a new program from a set of instructions and a
// reader which can be used to fetch the program's data and executes it.
func (mdm *MDM) ExecuteProgram(ctx context.Context, pt modules.RPCPriceTable, instructions []modules.Instruction, budget, collateralBudget types.Currency, sos StorageObligationSnapshot, programDataLen uint64, data io.Reader) (func(so StorageObligation) error, <-chan Output, error) {
	p := &Program{
		outputChan: make(chan Output, len(instructions)),
		staticProgramState: &programState{
			blockHeight: mdm.host.BlockHeight(),
			host:        mdm.host,
			priceTable:  pt,
			sectors:     newSectors(sos.SectorRoots()),
		},
		staticBudget:           budget,
		staticCollateralBudget: collateralBudget,
		staticData:             openProgramData(data, programDataLen),
		tg:                     &mdm.tg,
	}

	// Convert the instructions.
	var err error
	for _, i := range instructions {
		instruction, err := decodeInstruction(p, i)
		if err != nil {
			return nil, nil, errors.Compose(err, p.staticData.Close())
		}
		p.instructions = append(p.instructions, instruction)
	}
	// Increment the execution cost of the program.
	err = p.addCost(modules.MDMInitCost(pt, p.staticData.Len()))
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
		p.outputErr = p.executeInstructions(ctx, sos.ContractSize(), sos.MerkleRoot())
	}()
	// If the program is readonly there is no need to finalize it.
	if p.readOnly() {
		return nil, p.outputChan, nil
	}
	return p.managedFinalize, p.outputChan, nil
}

// addCollateral increases the collateral of the program by 'collateral'. If as
// a result the collateral becomes larger than the collateral budget of the
// program, an error is returned.
func (p *Program) addCollateral(collateral types.Currency) error {
	newCollateral := p.newCollateral.Add(collateral)
	if p.staticCollateralBudget.Cmp(newCollateral) < 0 {
		return modules.ErrMDMInsufficientCollateralBudget
	}
	p.newCollateral = newCollateral
	return nil
}

// addCost increases the cost of the program by 'cost'. If as a result the cost
// becomes larger than the budget of the program, ErrInsufficientBudget is
// returned.
func (p *Program) addCost(cost types.Currency) error {
	newExecutionCost := p.executionCost.Add(cost)
	if p.staticBudget.Cmp(newExecutionCost) < 0 {
		return modules.ErrMDMInsufficientBudget
	}
	p.executionCost = newExecutionCost
	return nil
}

// executeInstructions executes the programs instructions sequentially while
// returning the results to the caller using outputChan.
func (p *Program) executeInstructions(ctx context.Context, fcSize uint64, fcRoot crypto.Hash) error {
	output := output{
		NewSize:       fcSize,
		NewMerkleRoot: fcRoot,
	}
	for _, i := range p.instructions {
		select {
		case <-ctx.Done(): // Check for interrupt
			p.outputChan <- outputFromError(ErrInterrupted, p.newCollateral, p.executionCost, p.potentialRefund)
			break
		default:
		}
		// Add the memory the next instruction is going to allocate to the
		// total.
		p.usedMemory += i.Memory()
		time, err := i.Time()
		if err != nil {
			p.outputChan <- outputFromError(err, p.newCollateral, p.executionCost, p.potentialRefund)
		}
		memoryCost := modules.MDMMemoryCost(p.staticProgramState.priceTable, p.usedMemory, time)
		// Get the instruction cost and refund.
		instructionCost, refund, err := i.Cost()
		if err != nil {
			p.outputChan <- outputFromError(err, p.newCollateral, p.executionCost, p.potentialRefund)
			return err
		}
		cost := memoryCost.Add(instructionCost)
		// Increment the cost.
		err = p.addCost(cost)
		if err != nil {
			p.outputChan <- outputFromError(err, p.newCollateral, p.executionCost, p.potentialRefund)
			return err
		}
		// Add the instruction's potential refund to the total.
		p.potentialRefund = p.potentialRefund.Add(refund)
		// Increment collateral.
		collateral := i.Collateral()
		err = p.addCollateral(collateral)
		if err != nil {
			p.outputChan <- outputFromError(err, p.newCollateral, p.executionCost, p.potentialRefund)
			return err
		}
		// Execute next instruction.
		output = i.Execute(output)
		p.outputChan <- Output{
			output:          output,
			ExecutionCost:   p.executionCost,
			NewCollateral:   p.newCollateral,
			PotentialRefund: p.potentialRefund,
		}
		// Abort if the last output contained an error.
		if output.Error != nil {
			return output.Error
		}
	}
	return nil
}

// managedFinalize commits the changes made by the program to disk. It should
// only be called after the channel returned by Execute is closed.
func (p *Program) managedFinalize(so StorageObligation) error {
	// Prevent finalizing the program when it was aborted due to a failure.
	if p.outputErr != nil {
		return errors.Compose(p.outputErr, errors.New("can't call finalize on program that was aborted due to an error"))
	}
	// Compute the memory cost of finalizing the program.
	memoryCost := p.staticProgramState.priceTable.MemoryTimeCost.Mul64(p.usedMemory * modules.MDMTimeCommit)
	err := p.addCost(memoryCost)
	if err != nil {
		return err
	}
	// Commit the changes to the storage obligation.
	s := p.staticProgramState.sectors
	err = so.Update(s.merkleRoots, s.sectorsRemoved, s.sectorsGained)
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
