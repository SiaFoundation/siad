package mdm

import (
	"crypto"
	"errors"

	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
)

// Instruction is the interface an instruction needs to implement to be part of
// a program.
type Instruction interface {
	Cost() Cost
	Execute() Output
	ReadOnly() bool
}

// Output is the type returned by all instructions when being executed.
type Output struct {
	// The error will be set to nil unless the instruction experienced an error
	// during execution. If the instruction did experience an error during
	// execution, the program will halt at this instruction and no changes will
	// be committed.
	//
	// The error will be prefixed by 'invalid' if the error resulted from an
	// invalid program, and the error will be prefixed by 'hosterr' if the error
	// resulted from a host error such as a disk failure.
	Error error

	// NewSize is the size of a file contract after the execution of an
	// instruction.
	NewSize uint64

	// NewMerkleRoot is the merkle root after the execution of an instruction.
	NewMerkleRoot crypto.Hash

	// The output will be set to nil unless the instruction produces output for
	// the caller. One example of such an instruction would be 'Read()'. If
	// there was an error during execution, the output will be nil.
	Output []byte

	// The proof will be set to nil if there was an error, and also if no proof
	// was requested by the caller. Using only the proof, the caller will be
	// able to compute the next Merkle root and size of the contract.
	Proof []crypto.Hash
}

// commonInstruction contains all the fields shared by every instruction.
type commonInstruction struct {
	staticMerkleProof  bool
	staticContractSize uint64 // contract size before executing instruction
	staticFCID         types.FileContractID
	staticData         *ProgramData
}

// outputFromError is a convenience function to wrap an error in an Output.
func outputFromError(err error) Output {
	return Output{
		Error: err,
	}
}

// ****************************************************************************
//
// The folowing sections contain the supportd transactions of the MDM.
//
// ****************************************************************************

type instructionRead struct {
	commonInstruction

	lengthOff uint64
	offsetOff uint64
}

// NewReadInstruction creates a new read instructions from the provided
// operands.
func (p *Program) NewReadInstruction(offsetOff, lengthOff uint64, merkleProof bool) Instruction {
	return &instructionRead{
		commonInstruction: commonInstruction{
			staticContractSize: p.finalContractSize,
			staticFCID:         p.staticFCID,
			staticData:         p.staticData,
			staticMerkleProof:  merkleProof,
		},
		lengthOff: lengthOff,
		offsetOff: offsetOff,
	}
}

// Cost returns the cost of executing this instruction.
func (i *instructionRead) Cost() Cost {
	return ReadCost(i.staticContractSize)
}

// Execute execute the 'Read' instruction.
func (i *instructionRead) Execute() Output {
	// Fetch the operands.
	length, err := i.staticData.Uint64(i.lengthOff)
	if err != nil {
		return outputFromError(err)
	}
	offset, err := i.staticData.Uint64(i.offsetOff)
	if err != nil {
		return outputFromError(err)
	}
	// Validate the request.
	switch {
	case offset+length > modules.SectorSize:
		err = errors.New("request is out of bounds")
	case length == 0:
		err = errors.New("length cannot be zero")
	case i.staticMerkleProof && (offset%crypto.SegmentSize != 0 || length%crypto.SegmentSize != 0):
		err = errors.New("offset and length must be multiples of SegmentSize when requesting a Merkle proof")
	}
	if err != nil {
		s.writeError(err)
		return err
	}

	// enter response loop
	for i, sec := range req.Sections {
		// Fetch the requested data.
		sectorData, err := h.ReadSector(sec.MerkleRoot)
		if err != nil {
			s.writeError(err)
			return err
		}
		data := sectorData[sec.Offset : sec.Offset+sec.Length]

		// Construct the Merkle proof, if requested.
		var proof []crypto.Hash
		if req.MerkleProof {
			proofStart := int(sec.Offset) / crypto.SegmentSize
			proofEnd := int(sec.Offset+sec.Length) / crypto.SegmentSize
			proof = crypto.MerkleRangeProof(sectorData, proofStart, proofEnd)
		}

		// Send the response. If the renter sent a stop signal, or this is the
		// final response, include our signature in the response.
		resp := modules.LoopReadResponse{
			Signature:   nil,
			Data:        data,
			MerkleProof: proof,
		}
		select {
		case err := <-stopSignal:
			if err != nil {
				return err
			}
			resp.Signature = hostSig
			return s.writeResponse(resp)
		default:
		}
		if i == len(req.Sections)-1 {
			resp.Signature = hostSig
		}
		if err := s.writeResponse(resp); err != nil {
			return err
		}
	}
	// The stop signal must arrive before RPC is complete.
	return <-stopSignal
}

// ReadOnly for the 'Read' instruction is 'true'.
func (i *instructionRead) ReadOnly() bool {
	return true
}

// ****************************************************************************
