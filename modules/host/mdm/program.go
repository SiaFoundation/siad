package mdm

import (
	"context"
	"fmt"
	"io"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/threadgroup"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

var (
	// ErrEmptyProgram is returned if the program doesn't contain any instructions.
	ErrEmptyProgram = errors.New("can't execute program without instructions")

	// ErrInterrupted indicates that the program was interrupted during
	// execution and couldn't finish.
	ErrInterrupted = errors.New("execution of program was interrupted")
)

// FnFinalize is the type of a function returned by ExecuteProgram to finalize
// the changes made by the program.
type FnFinalize func(StorageObligation) error

// programState contains some fields needed for the execution of instructions.
// The program's state is captured when the program is created and remains the
// same during the execution of the program.
type programState struct {
	// host related fields
	host                    Host
	staticRevisionTxn       types.Transaction
	staticRemainingDuration types.BlockHeight

	// program cache
	sectors sectors

	// statistic related fields
	potentialStorageRevenue types.Currency
	riskedCollateral        types.Currency
	potentialUploadRevenue  types.Currency

	// budget related fields
	priceTable *modules.RPCPriceTable
}

// program is a collection of instructions. Within a program, each instruction
// will potentially modify the size and merkle root of a file contract. After the
// final instruction is executed, the MDM will create an updated revision of the
// FileContract which has to be signed by the renter and the host.
type program struct {
	instructions       []instruction
	staticData         *programData
	staticProgramState *programState

	staticBudget           *modules.RPCBudget
	staticCollateralBudget types.Currency
	executionCost          types.Currency
	additionalCollateral   types.Currency // collateral the host is required to add
	additionalStorageCost  types.Currency // cost of additional storage. This is refunded if the program doesn't commit.
	usedMemory             uint64

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

		ExecutionCost:         cost,
		AdditionalCollateral:  collateral,
		AdditionalStorageCost: refund,
	}
}

// decodeInstruction creates a specific instance of an instruction from a
// specified generic instruction.
func decodeInstruction(p *program, i modules.Instruction) (instruction, error) {
	switch i.Specifier {
	case modules.SpecifierAppend:
		return p.staticDecodeAppendInstruction(i)
	case modules.SpecifierDropSectors:
		return p.staticDecodeDropSectorsInstruction(i)
	case modules.SpecifierHasSector:
		return p.staticDecodeHasSectorInstruction(i)
	case modules.SpecifierReadSector:
		return p.staticDecodeReadSectorInstruction(i)
	case modules.SpecifierReadOffset:
		return p.staticDecodeReadOffsetInstruction(i)
	case modules.SpecifierRevision:
		return p.staticDecodeRevisionInstruction(i)
	case modules.SpecifierSwapSector:
		return p.staticDecodeSwapSectorInstruction(i)
	case modules.SpecifierUpdateRegistry:
		return p.staticDecodeUpdateRegistryInstruction(i)
	default:
		return nil, fmt.Errorf("unknown instruction specifier: %v", i.Specifier)
	}
}

// ExecuteProgram initializes a new program from a set of instructions and a
// reader which can be used to fetch the program's data and executes it.
func (mdm *MDM) ExecuteProgram(ctx context.Context, pt *modules.RPCPriceTable, p modules.Program, budget *modules.RPCBudget, collateralBudget types.Currency, sos StorageObligationSnapshot, duration types.BlockHeight, programDataLen uint64, data io.Reader) (_ FnFinalize, _ <-chan Output, err error) {
	// Sanity check program length.
	if len(p) == 0 {
		return nil, nil, ErrEmptyProgram
	}
	// Derive a new context to use and close it on error.
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	// Build program.
	program := &program{
		outputChan: make(chan Output),
		staticProgramState: &programState{
			staticRemainingDuration: duration,
			host:                    mdm.host,
			priceTable:              pt,
			sectors:                 newSectors(sos.SectorRoots()),
			staticRevisionTxn:       sos.RevisionTxn(),
		},
		staticBudget:           budget,
		usedMemory:             modules.MDMInitMemory(),
		staticCollateralBudget: collateralBudget,
		staticData:             openProgramData(data, programDataLen),
		tg:                     &mdm.tg,
	}
	// Convert the instructions.
	for _, i := range p {
		instruction, err := decodeInstruction(program, i)
		if err != nil {
			return nil, nil, errors.Compose(err, program.staticData.Close())
		}
		program.instructions = append(program.instructions, instruction)
	}
	// Increment the execution cost of the program.
	err = program.addCost(modules.MDMInitCost(pt, program.staticData.Len(), uint64(len(program.instructions))))
	if err != nil {
		return nil, nil, errors.Compose(err, program.staticData.Close())
	}
	// Execute all the instructions.
	if err := program.tg.Add(); err != nil {
		return nil, nil, errors.Compose(err, program.staticData.Close())
	}
	go func() {
		defer cancel()
		defer func() {
			err = errors.Compose(err, program.staticData.Close())
		}()
		defer program.tg.Done()
		defer close(program.outputChan)
		program.outputErr = program.executeInstructions(ctx, sos.ContractSize(), sos.MerkleRoot())
	}()
	// If the program is readonly there is no need to finalize it.
	if p.ReadOnly() {
		return nil, program.outputChan, nil
	}
	return program.managedFinalize, program.outputChan, nil
}

// addCollateral increases the collateral of the program by 'collateral'. If as
// a result the collateral becomes larger than the collateral budget of the
// program, an error is returned.
func (p *program) addCollateral(collateral types.Currency) error {
	additionalCollateral := p.additionalCollateral.Add(collateral)
	if p.staticCollateralBudget.Cmp(additionalCollateral) < 0 {
		return modules.ErrMDMInsufficientCollateralBudget
	}
	p.additionalCollateral = additionalCollateral
	return nil
}

// addCost increases the cost of the program by 'cost'. If as a result the cost
// becomes larger than the budget of the program, ErrInsufficientBudget is
// returned.
func (p *program) addCost(cost types.Currency) error {
	if !p.staticBudget.Withdraw(cost) {
		return modules.ErrMDMInsufficientBudget
	}
	p.executionCost = p.executionCost.Add(cost)
	return nil
}

// executeInstructions executes the programs instructions sequentially while
// returning the results to the caller using outputChan.
func (p *program) executeInstructions(ctx context.Context, fcSize uint64, fcRoot crypto.Hash) error {
	output := output{
		NewSize:       fcSize,
		NewMerkleRoot: fcRoot,
	}
	for idx, i := range p.instructions {
		select {
		case <-ctx.Done(): // Check for interrupt
			p.outputChan <- outputFromError(ErrInterrupted, p.additionalCollateral, p.executionCost, p.additionalStorageCost)
			return ErrInterrupted
		default:
		}
		// Increment collateral first.
		collateral := i.Collateral()
		err := p.addCollateral(collateral)
		if err != nil {
			p.outputChan <- outputFromError(err, p.additionalCollateral, p.executionCost, p.additionalStorageCost)
			return err
		}
		// Add the memory the next instruction is going to allocate to the
		// total.
		p.usedMemory += i.Memory()
		time, err := i.Time()
		if err != nil {
			p.outputChan <- outputFromError(err, p.additionalCollateral, p.executionCost, p.additionalStorageCost)
		}
		memoryCost := modules.MDMMemoryCost(p.staticProgramState.priceTable, p.usedMemory, time)
		// Get the instruction cost and storageCost.
		instructionCost, storageCost, err := i.Cost()
		if err != nil {
			p.outputChan <- outputFromError(err, p.additionalCollateral, p.executionCost, p.additionalStorageCost)
			return err
		}
		cost := memoryCost.Add(instructionCost)
		// Increment the cost.
		err = p.addCost(cost)
		if err != nil {
			p.outputChan <- outputFromError(err, p.additionalCollateral, p.executionCost, p.additionalStorageCost)
			return err
		}
		// Add the instruction's potential refund to the total.
		p.additionalStorageCost = p.additionalStorageCost.Add(storageCost)
		// Figure out whether to recommend the caller to batch this instruction
		// with the next one. We batch if the instruction is supposed to be
		// batched and if it's not the last instruction in the program.
		batch := idx < len(p.instructions)-1 && p.instructions[idx+1].Batch()
		// Execute next instruction.
		output = i.Execute(output)
		p.outputChan <- Output{
			output:                output,
			Batch:                 batch,
			ExecutionCost:         p.executionCost,
			AdditionalCollateral:  p.additionalCollateral,
			AdditionalStorageCost: p.additionalStorageCost,
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
func (p *program) managedFinalize(so StorageObligation) error {
	// Prevent finalizing the program when it was aborted due to a failure.
	if p.outputErr != nil {
		return errors.Compose(p.outputErr, errors.New("can't call finalize on program that was aborted due to an error"))
	}
	// Compute the memory cost of finalizing the program.
	memoryCost := modules.MDMMemoryCost(p.staticProgramState.priceTable, p.usedMemory, modules.MDMTimeCommit)
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
