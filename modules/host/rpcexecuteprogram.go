package host

import (
	"context"
	"net"

	"gitlab.com/NebulousLabs/Sia/build"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

// managedRPCExecute program handles incoming ExecuteProgram RPCs.
func (h *Host) managedRPCExecuteProgram(stream net.Conn) error {
	defer stream.Close()
	// Process payment.
	// TODO: change once payment MR is merged
	amountPaid := types.ZeroCurrency

	// Read request
	var epr modules.RPCExecuteProgramRequest
	err := modules.RPCRead(stream, &epr)
	if err != nil {
		return errors.AddContext(err, "Failed to read RPCExecuteProgramRequest")
	}

	// Extract the arguments.
	fcid, program, _, dataLength := epr.FileContractID, epr.Program, epr.PriceTableID, epr.ProgramDataLength

	// Get price table.
	// TODO: change once price table MR is merged to get the negotiated table.
	h.mu.RLock()
	pt := h.priceTable
	h.mu.RUnlock()

	// If the program isn't readonly we need to acquire the storage obligation.
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

	// Get a context that can be used to interrupt the program.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rpcDone := make(chan struct{})
	go func() {
		// TODO (followup): In the future we might want to wait for a signal
		// from the renter and close the context here early.
		select {
		case <-rpcDone:
		}
	}()

	// Execute the program.
	finalize, outputs, err := h.staticMDM.ExecuteProgram(ctx, pt, program, amountPaid, sos, dataLength, stream)
	if err != nil {
		return errors.AddContext(err, "Failed to start execution of the program")
	}

	// Charge the peer accordingly.
	cost := types.ZeroCurrency
	executionFailed := false
	refund := amountPaid
	defer func() {
		toCharge := cost
		if executionFailed || err != nil {
			// If the program execution failed or the RPC aborted for a different
			// reason, refund the peer.
			toCharge = toCharge.Sub(refund)
		}
		// TODO: Update EA balance.
	}()

	// Handle outputs.
	numOutputs := 0
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
			Error:           output.Error,
			NewMerkleRoot:   output.NewMerkleRoot,
			NewSize:         output.NewSize,
			Output:          output.Output,
			PotentialRefund: output.PotentialRefund,
			Proof:           output.Proof,
			TotalCost:       output.ExecutionCost,
		}
		// Update cost and refund.
		cost = output.ExecutionCost
		refund = output.PotentialRefund
		// Remember that the execution wasn't successful.
		executionFailed = resp.Error != nil
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
		so, err := h.managedGetStorageObligation(fcid)
		if err != nil {
			return errors.AddContext(err, "Failed to get storage obligation for finalizing the program")
		}
		err = finalize(so)
		if err != nil {
			return errors.AddContext(err, "Failed to finalize the program")
		}
		// Set the refund to 0. The program was finalized and we don't want to
		// refund the renter in the deferred statement anymore. This is a
		// precaution in case we extend the code after this point.
		refund = types.ZeroCurrency
	}
	// TODO: finalize spending for readonly programs once the MR is ready.
	return nil
}
