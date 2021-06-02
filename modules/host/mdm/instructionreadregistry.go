package mdm

import (
	"encoding/binary"
	"fmt"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionReadRegistry defines an instruction to read an entry from the
// registry.
type instructionReadRegistry struct {
	commonInstruction

	pubKeyOffset uint64
	pubKeyLength uint64
	tweakOffset  uint64

	staticType modules.ReadRegistryVersion
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
	if len(instruction.Args) != modules.RPCIReadRegistryLen &&
		len(instruction.Args) != modules.RPCIReadRegistryWithVersionLen {
		return nil, fmt.Errorf("expected instruction to have len %v or %v but was %v",
			modules.RPCIReadRegistryLen, modules.RPCIReadRegistryWithVersionLen, len(instruction.Args))
	}
	// Read args.
	pubKeyOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	pubKeyLength := binary.LittleEndian.Uint64(instruction.Args[8:16])
	tweakOffset := binary.LittleEndian.Uint64(instruction.Args[16:24])
	iType := modules.ReadRegistryVersionNoType
	if len(instruction.Args) == modules.RPCIReadRegistryWithVersionLen {
		iType = modules.ReadRegistryVersion(instruction.Args[24])
	}
	return &instructionReadRegistry{
		commonInstruction: commonInstruction{
			staticData:  p.staticData,
			staticState: p.staticProgramState,
		},
		pubKeyOffset: pubKeyOffset,
		pubKeyLength: pubKeyLength,
		tweakOffset:  tweakOffset,
		staticType:   iType,
	}, nil
}

// executeReadRegistry executes a registry lookup.
func executeReadRegistry(prevOutput output, ps *programState, sid modules.RegistryEntryID, needPubKeyAndTweak bool, version modules.ReadRegistryVersion) (output, types.Currency) {
	// Check version.
	switch version {
	case modules.ReadRegistryVersionNoType:
	case modules.ReadRegistryVersionWithType:
	default:
		return errOutput(errors.New("invalid read registry type")), types.ZeroCurrency
	}

	// Prepare the output. An empty output.Output means the data wasn't found.
	out := output{
		NewSize:       prevOutput.NewSize,
		NewMerkleRoot: prevOutput.NewMerkleRoot,
		Output:        nil,
	}

	// Get the value. If this fails we are done.
	spk, rv, found := ps.host.RegistryGet(sid)
	if !found {
		_, refund := modules.MDMReadRegistryCost(ps.priceTable)
		return out, refund
	}

	// Prepend the pubkey and tweak if necessary.
	if needPubKeyAndTweak {
		out.Output = append(out.Output, encoding.Marshal(spk)...)
		out.Output = append(out.Output, rv.Tweak[:]...)
	}

	// Return the signature followed by the data.
	rev := make([]byte, 8)
	binary.LittleEndian.PutUint64(rev, rv.Revision)
	out.Output = append(out.Output, rv.Signature[:]...)
	out.Output = append(out.Output, rev...)
	out.Output = append(out.Output, rv.Data...)
	if version == modules.ReadRegistryVersionWithType {
		out.Output = append(out.Output, byte(rv.Type))
	}
	return out, types.ZeroCurrency
}

// Execute executes the 'ReadRegistry' instruction.
func (i *instructionReadRegistry) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the args.
	pubKey, err := i.staticData.SiaPublicKey(i.pubKeyOffset, i.pubKeyLength)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	tweak, err := i.staticData.Hash(i.tweakOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	return executeReadRegistry(prevOutput, i.staticState, modules.DeriveRegistryEntryID(pubKey, tweak), false, i.staticType)
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
func (i *instructionReadRegistry) Cost() (executionCost, refund types.Currency, err error) {
	executionCost, refund = modules.MDMReadRegistryCost(i.staticState.priceTable)
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
