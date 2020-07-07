package mdm

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/encoding"
)

// instructionRevision returns the FileContractRevision returned by this MDM
// program.
type instructionRevision struct {
	commonInstruction
}

// staticDecodeRevisionInstruction creates a new 'Revision' instruction from
// the provided generic instruction.
func (p *program) staticDecodeRevisionInstruction(instruction modules.Instruction) (instruction, error) {
	// Check specifier.
	if instruction.Specifier != modules.SpecifierRevision {
		return nil, fmt.Errorf("expected specifier %v but got %v",
			modules.SpecifierRevision, instruction.Specifier)
	}
	// Check args.
	if len(instruction.Args) != modules.RPCIRevisionLen {
		return nil, fmt.Errorf("expected instruction to have len %v but was %v",
			modules.RPCIRevisionLen, len(instruction.Args))
	}
	return &instructionRevision{
		commonInstruction: commonInstruction{
			staticData:        p.staticData,
			staticMerkleProof: false,
			staticState:       p.staticProgramState,
		},
	}, nil
}

// Collateral is zero for the Revision instruction.
func (i *instructionRevision) Collateral() types.Currency {
	return modules.MDMRevisionCollateral()
}

// Cost returns the cost of executing this instruction.
func (i *instructionRevision) Cost() (executionCost, _ types.Currency, err error) {
	executionCost = modules.MDMRevisionCost(i.staticState.priceTable)
	return
}

// Memory returns the memory allocated by this instruction beyond the end of its
// lifetime.
func (i *instructionRevision) Memory() uint64 {
	return modules.MDMRevisionMemory()
}

// Execute executes the 'Revision' instruction.
func (i *instructionRevision) Execute(prevOutput output) output {
	// Fetch the requested information.
	revTxn := i.staticState.staticRevisionTxn
	fcTxn := i.staticState.staticFCTxn
	rev := revTxn.FileContractRevisions[0]
	renterSig := revTxn.TransactionSignatures[0]

	return output{
		NewSize:       prevOutput.NewSize,       // size stays the same
		NewMerkleRoot: prevOutput.NewMerkleRoot, // root stays the same
		Output: encoding.Marshal(modules.MDMInstructionRevisionResponse{
			Revision:        rev,
			RenterSig:       renterSig,
			SiacoinOutputID: fcTxn.SiacoinInputs[0].ParentID,
		}),
	}
}

// Time returns the execution time of a 'Revision' instruction.
func (i *instructionRevision) Time() (uint64, error) {
	return modules.MDMTimeRevision, nil
}
