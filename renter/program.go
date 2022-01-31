package renter

import (
	"errors"
	"fmt"
	"io"

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

	// ExecuteFunc is called for each executed instruction. The passed reader is
	// only valid until the next call to ExecuteFunc. Returning an error
	// terminates remote program execution. The program must be fully executed
	// for any changes to be committed. If an instruction fails, the entire
	// program is rolled back.
	ExecuteFunc func(rhp.RPCExecuteInstrResponse, io.Reader) error
)

// ExecuteProgram executes the program on the renter. execute is called for each
// successfully executed instruction; the reader passed to execute is only valid
// until the next call. Returning an error terminates remote program execution.
// The program must be fully executed for any changes to be committed. If an
// instruction fails, the program is rolled back.
//
// ContractRevision and RenterKey may be nil if the program is not read-write.
func (s *Session) ExecuteProgram(program Program, input []byte, payment PaymentMethod, execute ExecuteFunc) error {
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

	id, _, err := s.currentSettings()
	if err != nil {
		return fmt.Errorf("failed to get settings: %w", err)
	}

	if err := rpc.WriteRequest(stream, rhp.RPCExecuteProgramID, &id); err != nil {
		return fmt.Errorf("failed to write request: %w", err)
	}

	if err := s.pay(stream, payment, program.Budget); err != nil {
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
	}

	if _, err = stream.Write(input); err != nil {
		return fmt.Errorf("failed to write program data: %w", err)
	}

	var response rhp.RPCExecuteInstrResponse
	for i := range program.Instructions {
		if err = rpc.ReadResponse(stream, &response); err != nil {
			return fmt.Errorf("failed to read execute program response %v: %w", i, err)
		}

		if response.Error != nil {
			return fmt.Errorf("program execution failed at instruction %v: %w", i, response.Error)
		}

		// limit the execute function to the size of the response
		// TODO: set a max output length per instruction?
		lr := io.LimitReader(stream, int64(response.OutputLength))
		if execute != nil {
			if err := execute(response, lr); err != nil {
				return fmt.Errorf("failed to execute instruction %v: %w", i, err)
			}
		}

		// discard the remaining output in case the execute function didn't
		// consume it.
		io.Copy(io.Discard, lr)
	}

	if !program.RequiresFinalization {
		return nil
	}

	// finalize the program by updating the contract revision with additional
	// collateral.
	transfer := response.AdditionalCollateral.Add(response.AdditionalStorage)
	rev := program.Contract.Revision
	if rev.MissedHostOutput.Value.Cmp(transfer) < 0 {
		return errors.New("not enough collateral to finalize program")
	}

	rev.RevisionNumber++
	rev.FileMerkleRoot = response.NewMerkleRoot
	rev.Filesize = response.NewDataSize
	rev.MissedHostOutput.Value = rev.MissedHostOutput.Value.Sub(transfer)

	vc, err := s.cm.TipContext()
	if err != nil {
		return fmt.Errorf("failed to get current validation context: %w", err)
	}

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
	program.Contract.HostSignature = resp.Signature
	program.Contract.RenterSignature = req.Signature

	return nil
}
