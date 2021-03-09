package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instructionReadRegistrySID defines an instruction to read an entry from the
// registry by SubscriptionID.
type instructionReadRegistrySID struct {
	commonInstruction

	subscriptionIDOffset uint64
}

// staticDecodeReadRegistrySIDInstruction creates a new 'ReadRegistrySID'
// instruction from the provided generic instruction.
func (p *program) staticDecodeReadRegistrySIDInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadRegistrySID {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadRegistrySID, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadRegistrySIDLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIReadRegistrySIDLen, len(instruction.Args))
	}
	// Read args.
	sidOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	return &instructionReadRegistrySID{
		commonInstruction: commonInstruction{
			staticData:  p.staticData,
			staticState: p.staticProgramState,
		},
		subscriptionIDOffset: sidOffset,
	}, nil
}

// Execute executes the 'ReadRegistrySID' instruction.
func (i *instructionReadRegistrySID) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the args.
	sid, err := i.staticData.Hash(i.subscriptionIDOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	return executeReadRegistry(prevOutput, i.staticState, modules.SubscriptionID(sid))
}

// Registry reads can be batched, because they are both tiny, and low latency.
// Typical case is an in-memory lookup, worst case is a small, single on-disk
// read.
func (i *instructionReadRegistrySID) Batch() bool {
	return true
}

// Collateral returns the collateral the host has to put up for this
// instruction.
func (i *instructionReadRegistrySID) Collateral() types.Currency {
	return modules.MDMReadRegistryCollateral()
}

// Cost returns the Cost of this `ReadRegistrySID` instruction.
func (i *instructionReadRegistrySID) Cost() (executionCost, refund types.Currency, err error) {
	executionCost, refund = modules.MDMReadRegistryCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by the 'ReadRegistrySID' instruction beyond the
// lifetime of the instruction.
func (i *instructionReadRegistrySID) Memory() uint64 {
	return modules.MDMReadRegistryMemory()
}

// Time returns the execution time of an 'ReadRegistrySID' instruction.
func (i *instructionReadRegistrySID) Time() (uint64, error) {
	return modules.MDMTimeReadRegistry, nil
}
