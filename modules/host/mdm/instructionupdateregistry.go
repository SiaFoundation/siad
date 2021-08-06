package mdm

import (
	"encoding/binary"
	"fmt"

	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// instructionUpdateRegistry defines an update to a value in the host's
// registry.
type instructionUpdateRegistry struct {
	commonInstruction

	tweakOffset      uint64
	revisionOffset   uint64
	signatureOffset  uint64
	pubKeyOffset     uint64
	pubKeyLength     uint64
	dataOffset       uint64
	dataLen          uint64
	staticEntryType  modules.RegistryEntryType
	staticReturnType bool
}

// staticDecodeUpdateRegistryInstruction creates a new 'UpdateRegistry' instruction from the
// provided generic instruction.
func (p *program) staticDecodeUpdateRegistryInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierUpdateRegistry {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierUpdateRegistry, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIUpdateRegistryLen &&
		len(instruction.Args) != modules.RPCIUpdateRegistryWithVersionLen {
		return nil, fmt.Errorf("expected instruction to have len %v or %v but was %v",
			modules.RPCIUpdateRegistryLen, modules.RPCIUpdateRegistryWithVersionLen, len(instruction.Args))
	}
	// Read args.
	tweakOffset := binary.LittleEndian.Uint64(instruction.Args[:8])
	revisionOffset := binary.LittleEndian.Uint64(instruction.Args[8:16])
	signatureOffset := binary.LittleEndian.Uint64(instruction.Args[16:24])
	pubKeyOffset := binary.LittleEndian.Uint64(instruction.Args[24:32])
	pubKeyLength := binary.LittleEndian.Uint64(instruction.Args[32:40])
	dataOffset := binary.LittleEndian.Uint64(instruction.Args[40:48])
	dataLength := binary.LittleEndian.Uint64(instruction.Args[48:56])
	entryType := modules.RegistryTypeWithoutPubkey
	var returnType bool
	if len(instruction.Args) == modules.RPCIUpdateRegistryWithVersionLen {
		returnType = true
		entryType = modules.RegistryEntryType(instruction.Args[56])
	}
	return &instructionUpdateRegistry{
		commonInstruction: commonInstruction{
			staticData:  p.staticData,
			staticState: p.staticProgramState,
		},
		tweakOffset:      tweakOffset,
		revisionOffset:   revisionOffset,
		signatureOffset:  signatureOffset,
		pubKeyOffset:     pubKeyOffset,
		pubKeyLength:     pubKeyLength,
		dataOffset:       dataOffset,
		dataLen:          dataLength,
		staticEntryType:  entryType,
		staticReturnType: returnType,
	}, nil
}

// Batch declares whether or not this instruction can be batched together with
// the previous instruction.
func (i instructionUpdateRegistry) Batch() bool {
	return true
}

// Execute executes the 'UpdateRegistry' instruction.
func (i *instructionUpdateRegistry) Execute(prevOutput output) (output, types.Currency) {
	// Fetch the args.
	tweak, err := i.staticData.Hash(i.tweakOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	revision, err := i.staticData.Uint64(i.revisionOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	signature, err := i.staticData.Signature(i.signatureOffset)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	pubKey, err := i.staticData.SiaPublicKey(i.pubKeyOffset, i.pubKeyLength)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}
	data, err := i.staticData.Bytes(i.dataOffset, i.dataLen)
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	// Add 1 year to the expiry.
	newExpiry := i.staticState.host.BlockHeight() + types.BlocksPerYear

	// Try updating the registry.
	rv := modules.NewSignedRegistryValue(tweak, data, revision, signature, i.staticEntryType)
	existingRV, err := i.staticState.host.RegistryUpdate(rv, pubKey, newExpiry)
	if modules.IsRegistryEntryExistErr(err) {
		// If we weren't able to update the registry because the entry already
		// exists, we need to return a proof.
		rev := make([]byte, 8)
		binary.LittleEndian.PutUint64(rev, existingRV.Revision)
		out := output{
			NewSize:       prevOutput.NewSize,
			NewMerkleRoot: prevOutput.NewMerkleRoot,
			Output:        append(existingRV.Signature[:], append(rev, existingRV.Data...)...),
			Error:         err,
		}
		if i.staticReturnType {
			out.Output = append(out.Output, byte(existingRV.Type))
		}
		return out, types.ZeroCurrency
	}
	if err != nil {
		return errOutput(err), types.ZeroCurrency
	}

	return output{
		NewSize:       prevOutput.NewSize,
		NewMerkleRoot: prevOutput.NewMerkleRoot,
	}, types.ZeroCurrency
}

// Collateral returns the collateral the host has to put up for this
// instruction.
func (i *instructionUpdateRegistry) Collateral() types.Currency {
	return modules.MDMUpdateRegistryCollateral()
}

// Cost returns the Cost of this `UpdateRegistry` instruction.
func (i *instructionUpdateRegistry) Cost() (executionCost, storeCost types.Currency, err error) {
	executionCost, storeCost = modules.MDMUpdateRegistryCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by the 'UpdateRegistry' instruction beyond the
// lifetime of the instruction.
func (i *instructionUpdateRegistry) Memory() uint64 {
	return modules.MDMUpdateRegistryMemory()
}

// Time returns the execution time of an 'UpdateRegistry' instruction.
func (i *instructionUpdateRegistry) Time() (uint64, error) {
	return modules.MDMTimeUpdateRegistry, nil
}
