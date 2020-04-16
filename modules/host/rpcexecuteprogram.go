package host

import (
	"context"
	"fmt"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// managedRPCExecuteProgram handles incoming ExecuteProgram RPCs.
func (h *Host) managedRPCExecuteProgram(stream siamux.Stream) error {
	// read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return errors.AddContext(err, "Failed to read price table")
	}

	// Process payment.
	pd, err := h.ProcessPayment(stream)
	if err != nil {
		return errors.AddContext(err, "failed to process paymnet")
	}
	// Refund all the money we didn't use at the end of the RPC.
	refundAccount := pd.AccountID()
	amountPaid := pd.Amount()
	refund := amountPaid
	err = h.tg.Add()
	if err != nil {
		return err
	}
	defer func() {
		go func() {
			defer h.tg.Done()
			depositErr := h.staticAccountManager.callRefund(refundAccount, refund)
			if depositErr != nil {
				h.log.Print("ERROR: failed to refund renter", depositErr)
			}
		}()
	}()
	// Don't expect any added collateral.
	if !pd.AddedCollateral().IsZero() {
		return fmt.Errorf("no collateral should be moved but got %v", pd.AddedCollateral().HumanString())
	}

	// Read request
	var epr modules.RPCExecuteProgramRequest
	err = modules.RPCRead(stream, &epr)
	if err != nil {
		return errors.AddContext(err, "Failed to read RPCExecuteProgramRequest")
	}

	// Extract the arguments.
	fcid, program, dataLength := epr.FileContractID, epr.Program, epr.ProgramDataLength

	// If the program isn't readonly we need to acquire a lock on the storage
	// obligation.
	readonly := program.ReadOnly()
	if !readonly {
		h.managedLockStorageObligation(fcid)
		defer h.managedUnlockStorageObligation(fcid)
	}

	// Get a snapshot of the storage obligation.
	sos, err := h.managedGetStorageObligationSnapshot(fcid)
	if err != nil {
		return errors.AddContext(err, "Failed to get storage obligation snapshot")
	}

	// Get the remaining unallocated collateral.
	collateralBudget := sos.UnallocatedCollateral()

	// Get a context that can be used to interrupt the program.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// TODO (followup): In the future we might want to wait for a signal
		// from the renter and close the context here early.
		select {
		case <-ctx.Done():
		}
	}()

	// Execute the program.
	_, outputs, err := h.staticMDM.ExecuteProgram(ctx, pt, program, amountPaid, collateralBudget, sos, dataLength, stream)
	if err != nil {
		return errors.AddContext(err, "Failed to start execution of the program")
	}

	// Handle outputs.
	executionFailed := false
	numOutputs := 0
	cost := types.ZeroCurrency
	for output := range outputs {
		// Remember number of returned outputs.
		numOutputs++
		// Sanity check if one of the instructions already failed. This
		// shouldn't happen.
		if executionFailed {
			build.Critical("There shouldn't be another output after the execution already failed")
			continue // continue to drain the channel
		}
		// Prepare the RPC response.
		resp := modules.RPCExecuteProgramResponse{
			AdditionalCollateral: output.AdditionalCollateral,
			Error:                output.Error,
			NewMerkleRoot:        output.NewMerkleRoot,
			NewSize:              output.NewSize,
			Output:               output.Output,
			PotentialRefund:      output.PotentialRefund,
			Proof:                output.Proof,
			TotalCost:            output.ExecutionCost,
		}
		// Update cost and refund.
		if output.PotentialRefund.Cmp(output.ExecutionCost) < 0 {
			err = errors.New("executionCost can never be smaller than the refund")
			build.Critical(err)
			return err
		}
		cost = output.ExecutionCost
		refund = amountPaid.Add(output.PotentialRefund).Sub(cost)
		// Remember that the execution wasn't successful.
		executionFailed = output.Error != nil
		// Send the response to the peer.
		err = modules.RPCWrite(stream, resp)
		if err != nil {
			return errors.AddContext(err, "failed to send output to peer")
		}
	}
	// Sanity check that we received at least 1 output.
	if numOutputs == 0 {
		err := errors.New("program returned 0 outputs - should never happen")
		build.Critical(err)
		return err
	}

	// If the execution failed we return without an error. The peer will notice
	// the error in the last instruction and know that the communication is over
	// at this point. Nothing more to do than return the promised refund.
	if executionFailed {
		return nil
	}

	// Call finalize if the program is not readonly.
	if !readonly {
		// TODO: The program was not readonly which means the merkle root
		// changed. Sign a new revision with the correct root.
		// TODO: The revision needs to update the collateral if necessary.
		// TODO: The revision needs to update the storage payment.
		//
		//		so, err := h.managedGetStorageObligation(fcid)
		//		if err != nil {
		//			return errors.AddContext(err, "Failed to get storage obligation for finalizing the program")
		//		}
		//		err = finalize(so)
		//		if err != nil {
		//			return errors.AddContext(err, "Failed to finalize the program")
		//		}
		return errors.New("only readonly programs are supported right now")
	}
	//	else {
	//		// TODO: finalize spending for readonly programs once the MR is ready.
	//	}
	// The program was finalized and we don't want to refund the renter beyond
	// the difference between the paid amount and execution cost in the deferred
	// statement anymore. This is a precaution in case we extend the code after
	// this point.
	refund = amountPaid.Sub(cost)
	return nil
}
