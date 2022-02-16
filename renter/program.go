package renter

import (
	"errors"
	"fmt"
	"io"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

type (
	// A Program wraps a set of instructions, contract and a budget for
	// execution on a host. If RequiresContract or RequiresFinalization are
	// true, ContractRevision and RenterKey must also be set.
	Program struct {
		Instructions []rhp.Instruction
		Budget       types.Currency

		RequiresContract     bool
		RequiresFinalization bool
		Contract             *rhp.Contract
		RenterKey            types.PrivateKey
	}

	// OnOutputFunc is called for each executed instruction. The passed reader is
	// only valid until the next call to ExecuteFunc. Returning an error
	// terminates remote program execution. The program must be fully executed
	// for any changes to be committed. If an instruction fails, the entire
	// program is rolled back.
	OnOutputFunc func(rhp.RPCExecuteInstrResponse, io.Reader) error
)

// NoopOnOutput is an OnOutputFunc that ignores the output of a Program.
func NoopOnOutput(rhp.RPCExecuteInstrResponse, io.Reader) error { return nil }

// ExecuteProgram executes the program on the renter. execute is called for each
// successfully executed instruction; the reader passed to execute is only valid
// until the next call. Returning an error terminates remote program execution.
// The program must be fully executed for any changes to be committed. If an
// instruction fails, the program is rolled back.
//
// The Session's ContractRevision and RenterKey may be nil if the program does
// not require a contract or finalization.
func (s *Session) ExecuteProgram(program Program, input []byte, payment PaymentMethod, onOutput OnOutputFunc) error {
	if (program.RequiresContract || program.RequiresFinalization) && program.Contract == nil {
		return errors.New("contract required for read-write programs")
	} else if (program.RequiresContract || program.RequiresFinalization) && len(program.RenterKey) != 64 {
		return errors.New("contract key is required for read-write programs")
	}

	stream, err := s.session.DialStream()
	if err != nil {
		return fmt.Errorf("failed to open new stream: %w", err)
	}
	defer stream.Close()
	stream.SetDeadline(time.Now().Add(time.Minute))

	id, _, err := s.currentSettings()
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if err := rpc.WriteRequest(stream, rhp.RPCExecuteProgramID, &id); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	} else if err := s.pay(stream, payment, program.Budget); err != nil {
		return fmt.Errorf("failed to pay for program execution: %w", err)
	}

	var contractID types.ElementID
	if program.Contract != nil {
		contractID = program.Contract.ID
	}

	err = rpc.WriteResponse(stream, &rhp.RPCExecuteProgramRequest{
		FileContractID:    contractID,
		Instructions:      program.Instructions,
		ProgramDataLength: uint64(len(input)),
	})
	if err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	} else if _, err = stream.Write(input); err != nil {
		return fmt.Errorf("failed to write program data: %w", err)
	}

	var response rhp.RPCExecuteInstrResponse
	for i := range program.Instructions {
		// reset the deadline for each executed instruction.
		stream.SetDeadline(time.Now().Add(5 * time.Minute))
		if err = rpc.ReadResponse(stream, &response); err != nil {
			return fmt.Errorf("failed to read execute program response %v: %w", i, err)
		} else if response.Error != nil {
			return fmt.Errorf("program execution failed at instruction %v: %w", i, response.Error)
		}

		// limit the execute function to the size of the response
		// TODO: set a max output length per instruction?
		lr := io.LimitReader(stream, int64(response.OutputLength))
		if err := onOutput(response, lr); err != nil {
			return fmt.Errorf("failed to execute instruction %v: %w", i, err)
		}

		// discard the remaining output in case the execute function didn't
		// consume it.
		io.Copy(io.Discard, lr)
	}

	if !program.RequiresFinalization {
		return nil
	}

	// reset the deadline for contract finalization.
	stream.SetDeadline(time.Now().Add(2 * time.Minute))

	// Finalize the program by updating the contract revision with additional
	// collateral. The additional collateral and storage revenue must be
	// subtracted from the host's missed output to penalize the host on failure.
	transfer := response.AdditionalCollateral.Add(response.AdditionalStorage)
	rev, err := rhp.FinalizeProgramRevision(program.Contract.Revision, transfer)
	if err != nil {
		return fmt.Errorf("failed to revise contract: %w", err)
	}

	// update the filesize and Merkle root
	rev.Filesize = response.NewDataSize
	rev.FileMerkleRoot = response.NewMerkleRoot

	vc := s.cm.TipContext()
	contractHash := vc.ContractSigHash(rev)
	req := rhp.RPCFinalizeProgramRequest{
		Signature:         program.RenterKey.SignHash(contractHash),
		NewRevisionNumber: rev.RevisionNumber,
		NewOutputs: rhp.ContractOutputs{
			MissedHostValue:   rev.MissedHostOutput.Value,
			MissedRenterValue: rev.MissedRenterOutput.Value,
			ValidHostValue:    rev.ValidHostOutput.Value,
			ValidRenterValue:  rev.ValidRenterOutput.Value,
		},
	}

	if err := rpc.WriteResponse(stream, &req); err != nil {
		return fmt.Errorf("failed to write revision signing request: %w", err)
	}

	var resp rhp.RPCRevisionSigningResponse
	if err = rpc.ReadResponse(stream, &resp); err != nil {
		return fmt.Errorf("failed to read revision signing response: %w", err)
	}

	// verify the host signature
	if !s.hostKey.VerifyHash(contractHash, resp.Signature) {
		return errors.New("host revision signature is invalid")
	}

	program.Contract.Revision = rev
	program.Contract.Revision.HostSignature = resp.Signature
	program.Contract.Revision.RenterSignature = req.Signature

	return nil
}
