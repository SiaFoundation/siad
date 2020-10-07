package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// instructionReadRegistry defines an instruction to read an entry from the
// registry.
type instructionReadRegistry struct {
	commonInstruction

	pubKeyOffset uint64
	pubKeyLength uint64
	tweakOffset  uint64
}

// staticDecodeReadRegistryInstruction creates a new 'ReadRegistry' instruction
// from the provided generic instruction.
func (p *program) staticDecodeReadRegistryInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierReadRegistry {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierReadRegistry, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIReadRegistryLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIReadRegistryLen, len(instruction.Args))
	}
	// Read args.
	pubKeyOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	pubKeyLength := binary.LittleEndian.Uint64(instruction.Args[8:16])
	tweakOffset := binary.LittleEndian.Uint64(instruction.Args[16:24])
	return &instructionReadRegistry{
		commonInstruction: commonInstruction{
			staticData:  p.staticData,
			staticState: p.staticProgramState,
		},
		pubKeyOffset: pubKeyOffset,
		pubKeyLength: pubKeyLength,
		tweakOffset:  tweakOffset,
	}, nil
}

// Execute executes the 'ReadRegistry' instruction.
func (i *instructionReadRegistry) Execute(prevOutput output) output {
	// Fetch the args.
	pubKey, err := i.staticData.SiaPublicKey(i.pubKeyOffset, i.pubKeyLength)
	if err != nil {
		return errOutput(err)
	}
	tweak, err := i.staticData.Hash(i.tweakOffset)
	if err != nil {
		return errOutput(err)
	}

	// Get the value.
	rv, found := i.staticState.host.RegistryGet(pubKey, tweak)
	if !found {
		return errOutput(modules.ErrRegistryValueNotExist)
	}

	// Return the signature followed by the data.
	rev := make([]byte, 8)
	binary.LittleEndian.PutUint64(rev, rv.Revision)
	return output{
		NewSize:       prevOutput.NewSize,
		NewMerkleRoot: prevOutput.NewMerkleRoot,
		Output:        append(rv.Signature[:], append(rev, rv.Data...)...),
	}
}

// Registry reads can be batched, because they are both tiny, and low latency.
// Typical case is an in-memory lookup, worst case is a small, single on-disk
// read.
func (i *instructionReadRegistry) Batch() bool {
	return true
}

// Collateral returns the collateral the host has to put up for this
// instruction.
func (i *instructionReadRegistry) Collateral() types.Currency {
	return modules.MDMReadRegistryCollateral()
}

// Cost returns the Cost of this `ReadRegistry` instruction.
func (i *instructionReadRegistry) Cost() (executionCost, _ types.Currency, err error) {
	executionCost = modules.MDMReadRegistryCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by the 'ReadRegistry' instruction beyond the
// lifetime of the instruction.
func (i *instructionReadRegistry) Memory() uint64 {
	return modules.MDMReadRegistryMemory()
}

// Time returns the execution time of an 'ReadRegistry' instruction.
func (i *instructionReadRegistry) Time() (uint64, error) {
	return modules.MDMTimeReadRegistry, nil
}
