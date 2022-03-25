package host

import (
	"fmt"
	"io"
	"time"

	"go.sia.tech/core/host"
	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

func (r *rpcSession) rpcExecuteProgram() error {
	log := r.log.Scope("RPCExecute")
	r.stream.SetDeadline(time.Now().Add(time.Minute))

	var settingsID rhp.SettingsID
	if err := r.readRequest(&settingsID); err != nil {
		return fmt.Errorf("failed to read settings UID: %w", err)
	}

	settings, err := r.settings.valid(settingsID)
	if err != nil {
		err = fmt.Errorf("failed to get settings %v: %w", settingsID, err)
		log.Warnln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	} else if err = r.processPayment(); err != nil {
		err = fmt.Errorf("failed to process payment: %w", err)
		log.Warnln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}
	defer r.refund()

	// wrap the stream in a budget to pay for bandwidth usage, also serves as
	// a data limit since all usage is deducted from the budget.
	budgetedStream := host.NewBudgetedStream(r.stream, &r.budget, settings)

	// read the program
	var executeReq rhp.RPCExecuteProgramRequest
	err = rpc.ReadResponse(budgetedStream, &executeReq)
	if err != nil {
		err = fmt.Errorf("failed to read execute request: %w", err)
		log.Warnln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}

	var requiresContract, requiresFinalization bool
	for _, instr := range executeReq.Instructions {
		requiresContract = requiresContract || instr.RequiresContract()
		requiresFinalization = requiresFinalization || instr.RequiresFinalization()
	}

	vc := r.cm.TipContext()
	executor := host.NewExecutor(r.privkey, r.sectors, r.contracts, r.registry, vc, settings, &r.budget)
	// revert any changes to the host state that were made by the program. Also
	// adds the failure refund back to the budget for the deferred refund.
	defer executor.Revert()

	// If the program requires finalization or a contract, verify that the
	// contract is valid and lockable.
	if requiresFinalization || requiresContract {
		// lock the contract
		contract, err := r.contracts.Lock(executeReq.FileContractID, time.Second*10)
		if err != nil {
			err = fmt.Errorf("failed to lock contract %v: %w", executeReq.FileContractID, err)
			log.Warnln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		}
		defer r.contracts.Unlock(executeReq.FileContractID)

		// verify we can still modify the contract
		switch {
		case contract.Revision.RevisionNumber == types.MaxRevisionNumber:
			err = fmt.Errorf("cannot use contract %v: already finalized", executeReq.FileContractID)
			log.Warnln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		case vc.Index.Height >= contract.Revision.WindowStart:
			err = fmt.Errorf("cannot use contract %v: proof window has already started", executeReq.FileContractID)
			log.Warnln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		}

		if err := executor.SetContract(contract); err != nil {
			err = fmt.Errorf("failed to set contract %v: %w", executeReq.FileContractID, err)
			log.Errorln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		}
	}

	// subtract the initialization cost from the budget. Initialization costs
	// are not refunded if execution fails. Also includes finalization cost if
	// the program requires it.
	execCost := rhp.ExecutionCost(settings, executeReq.ProgramDataLength, uint64(len(executeReq.Instructions)), requiresFinalization)
	if err := r.budget.Spend(execCost.BaseCost); err != nil {
		err = fmt.Errorf("failed to pay execution cost %v: %w", execCost.BaseCost, err)
		log.Warnln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}

	// execute each instruction in the program, sending the output to the renter.
	// Execution is stopped on any error.
	lr := io.LimitReader(budgetedStream, int64(executeReq.ProgramDataLength))
	programUID := generateUniqueID()
	for i, instruction := range executeReq.Instructions {
		// reset the deadline for each instruction
		r.stream.SetDeadline(time.Now().Add(time.Minute))
		recordEnd := r.recordExecuteInstruction(programUID, instruction.Specifier())
		if err := executor.ExecuteInstruction(lr, budgetedStream, instruction); err != nil {
			err = fmt.Errorf("failed to execute instruction %v: %w", i, err)
			log.Warnln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		}
		recordEnd()
	}

	if !requiresFinalization {
		if err := executor.Commit(); err != nil {
			err = fmt.Errorf("failed to commit execution: %w", err)
			log.Errorln(err)
			rpc.WriteResponseErr(r.stream, err)
			return err
		}
		return nil
	}

	// reset the deadline for finalization
	r.stream.SetDeadline(time.Now().Add(time.Second * 30))

	// if the program requires finalization the contract must be updated with
	// additional collateral, roots, and filesize.
	var finalizeReq rhp.RPCFinalizeProgramRequest
	if err := rpc.ReadResponse(budgetedStream, &finalizeReq); err != nil {
		err = fmt.Errorf("failed to read finalize request: %w", err)
		log.Warnln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}

	contract, err := executor.FinalizeContract(finalizeReq)
	if err != nil {
		err = fmt.Errorf("failed to finalize contract %v: %w", contract.ID, err)
		r.log.Errorln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}

	// the program has successfully executed and finalized, commit any state
	// changes.
	if err := executor.Commit(); err != nil {
		err = fmt.Errorf("failed to commit execution: %w", err)
		r.log.Errorln(err)
		rpc.WriteResponseErr(r.stream, err)
		return err
	}

	err = rpc.WriteResponse(budgetedStream, &rhp.RPCRevisionSigningResponse{
		Signature: contract.Revision.HostSignature,
	})
	if err != nil {
		err = fmt.Errorf("failed to write host signature response: %w", err)
		r.log.Errorln(err)
		return err
	}
	return nil
}
