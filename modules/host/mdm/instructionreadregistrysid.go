package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionReadRegistryEID defines an instruction to read an entry from the
// registry by SubscriptionID.
type instructionReadRegistryEID struct {
	commonInstruction

	subscriptionIDOffset uint64
	needPubKeyAndTweak   bool

	staticType modules.ReadRegistryVersion
}

// staticDecodeReadRegistryEIDInstruction creates a new 'ReadRegistryEID'
// instruction from the provided generic instruction.
func (p *program) staticDecodeReadRegistryEIDInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadRegistryEID {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadRegistryEID, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadRegistryEIDLen &&
		len(instruction.Args) != modules.RPCIReadRegistryEIDWithVersionLen {
		return nil, fmt.Errorf("expected instruction to have len %v or %v but was %v",
			modules.RPCIReadRegistryEIDLen, modules.RPCIReadRegistryEIDWithVersionLen, len(instruction.Args))
	}
	// Read args.
	eidOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	needPubKeyAndTweak := instruction.Args[8] == 1
	iType := modules.ReadRegistryVersionNoType
	if len(instruction.Args) == modules.RPCIReadRegistryEIDWithVersionLen {
		iType = modules.ReadRegistryVersion(instruction.Args[9])
	}
	return &instructionReadRegistryEID{
		commonInstruction: commonInstruction{
			staticData:  p.staticData,
			staticState: p.staticProgramState,
		},
		needPubKeyAndTweak:   needPubKeyAndTweak,
		subscriptionIDOffset: eidOffset,
		staticType:           iType,
	}, nil
}

// Execute executes the 'ReadRegistryEID' instruction.
func (i *instructionReadRegistryEID) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the args.
	sid, err := i.staticData.Hash(i.subscriptionIDOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	return executeReadRegistry(prevOutput, i.staticState, modules.RegistryEntryID(sid), i.needPubKeyAndTweak, i.staticType)
}

// Registry reads can be batched, because they are both tiny, and low latency.
// Typical case is an in-memory lookup, worst case is a small, single on-disk
// read.
func (i *instructionReadRegistryEID) Batch() bool {
	return true
}

// Collateral returns the collateral the host has to put up for this
// instruction.
func (i *instructionReadRegistryEID) Collateral() types.Currency {
	return modules.MDMReadRegistryCollateral()
}

// Cost returns the Cost of this `ReadRegistryEID` instruction.
func (i *instructionReadRegistryEID) Cost() (executionCost, refund types.Currency, err error) {
	executionCost, refund = modules.MDMReadRegistryCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by the 'ReadRegistryEID' instruction beyond the
// lifetime of the instruction.
func (i *instructionReadRegistryEID) Memory() uint64 {
	return modules.MDMReadRegistryMemory()
}

// Time returns the execution time of an 'ReadRegistryEID' instruction.
func (i *instructionReadRegistryEID) Time() (uint64, error) {
	return modules.MDMTimeReadRegistry, nil
}
