package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/modules/host/mdm"
	"go.sia.tech/siad/types"
)

const (
	// maxRPCExecuteProgramRequestSize is the max size we allocate for
	// reading a RPCExecuteProgramRequest.
	maxRPCExecuteProgramRequestSize = 1 << 22 // 4 MiB

	// maxRPCExecuteProgramRevisionSigningRequestSize is the max size we
	// allocate for reading a RPCExecuteProgramRevisionSigningRequest.
	maxRPCExecuteProgramRevisionSigningRequestSize = 1 << 20 // 1 MiB
)

// managedRPCExecuteProgram handles incoming ExecuteProgram RPCs.
func (h *Host) managedRPCExecuteProgram(stream siamux.Stream) error {
	// read the price table
	pt, err := h.staticReadPriceTableID(stream)
	if err != nil {
		return errors.AddContext(err, "failed to read price table")
	}

	// Process payment.
	pd, err := h.ProcessPayment(stream, pt.HostBlockHeight)
	if err != nil {
		return errors.AddContext(err, "failed to process payment")
	}

	// Add limit to the stream. The readCost is the UploadBandwidthCost since
	// reading from the stream means uploading from the host's perspective. That
	// makes the writeCost the DownloadBandwidthCost.
	budget := modules.NewBudget(pd.Amount())
	bandwidthLimit := modules.NewBudgetLimit(budget, pt.UploadBandwidthCost, pt.DownloadBandwidthCost)
	err = stream.SetLimit(bandwidthLimit)
	if err != nil {
		return errors.AddContext(err, "failed to set budget limit on stream")
	}

	// Refund all the money we didn't use at the end of the RPC.
	refundAccount := pd.AccountID()
	programRefund := pd.Amount()
	err = h.tg.Add()
	if err != nil {
		return err
	}
	defer func() {
		go func() {
			defer h.tg.Done()
			// The total refund is the remaining value of the budget + the
			// potential program refund.
			depositErr := h.staticAccountManager.callRefund(refundAccount, programRefund.Add(budget.Remaining()))
			if depositErr != nil {
				h.log.Print("ERROR: failed to refund renter", depositErr)
			}
		}()
	}()

	// Read request
	var epr modules.RPCExecuteProgramRequest
	err = modules.RPCReadMaxLen(stream, &epr, maxRPCExecuteProgramRequestSize)
	if err != nil {
		return errors.AddContext(err, "Failed to read RPCExecuteProgramRequest")
	}

	// Extract the arguments.
	fcid, instructions, dataLength := epr.FileContractID, epr.Program, epr.ProgramDataLength
	program := modules.Program(instructions)

	// If the program isn't readonly we need to acquire a lock on the storage
	// obligation.
	readonly := program.ReadOnly()
	if !readonly {
		h.managedLockStorageObligation(fcid)
		defer h.managedUnlockStorageObligation(fcid)
	}

	// Get a snapshot of the storage obligation if required.
	sos := ZeroStorageObligationSnapshot()
	if program.RequiresSnapshot() {
		sos, err = h.managedGetStorageObligationSnapshot(fcid)
		if err != nil {
			return errors.AddContext(err, fmt.Sprintf("failed to get storage obligation snapshot for contract %v", fcid))
		}
	}

	// Get the remaining unallocated collateral.
	collateralBudget := sos.UnallocatedCollateral()

	// Get the remaining contract duration.
	bh := h.BlockHeight()
	duration := sos.ProofDeadline() - bh

	// Get a context that can be used to interrupt the program.
	ctx, cancel := context.WithCancel(h.tg.StopCtx())
	defer cancel()
	go func() {
		// TODO (followup): In the future we might want to wait for a signal
		// from the renter and close the context here early.
		select {
		case <-ctx.Done():
		}
	}()

	// Execute the program.
	finalize, outputs, err := h.staticMDM.ExecuteProgram(ctx, pt, program, budget, collateralBudget, sos, duration, dataLength, stream)
	if err != nil {
		return errors.AddContext(err, "Failed to start execution of the program")
	}

	// Create a buffer
	buffer := bytes.NewBuffer(nil)

	// Flush the buffer. Upon success this should be a no-op. If we return early
	// this will make sure that the cancellation token and anything else in the
	// buffer are written to the stream.
	defer func() {
		if buffer.Len() > 0 {
			_, err = buffer.WriteTo(stream)
			if err != nil {
				h.log.Print("failed to flush buffer", err)
			}
		}
	}()

	// Return 16 bytes of data as a placeholder for a future cancellation token.
	var ct modules.MDMCancellationToken
	err = modules.RPCWrite(buffer, ct)
	if err != nil {
		return errors.AddContext(err, "Failed to write cancellation token")
	}

	// Handle outputs.
	executionFailed := false
	numOutputs := 0
	var output mdm.Output
	for output = range outputs {
		// Remember number of returned outputs.
		numOutputs++
		// Sanity check if one of the instructions already failed. This
		// shouldn't happen.
		if executionFailed {
			build.Critical("There shouldn't be another output after the execution already failed")
			continue // continue to drain the channel
		}

		// Sanity check output when error occurred
		if executionFailed && len(output.Output) > 0 {
			err = fmt.Errorf("output.Error != nil but len(output.Output) == %v", len(output.Output))
			build.Critical(err) // don't return on purpose
		}

		// Prepare the RPC response.
		resp := modules.RPCExecuteProgramResponse{
			AdditionalCollateral: output.AdditionalCollateral,
			Error:                output.Error,
			NewMerkleRoot:        output.NewMerkleRoot,
			NewSize:              output.NewSize,
			OutputLength:         uint64(len(output.Output)),
			FailureRefund:        output.FailureRefund,
			Proof:                output.Proof,
			TotalCost:            output.ExecutionCost,
		}
		// Update cost and refund.
		if output.ExecutionCost.Cmp(output.FailureRefund) < 0 {
			err = errors.New("executionCost can never be smaller than the storage cost")
			build.Critical(err)
			return err
		}
		// The additional storage cost is refunded if the program is not
		// committed.
		programRefund = resp.FailureRefund
		// Remember that the execution wasn't successful.
		executionFailed = output.Error != nil

		// Send the response to the peer.
		err = modules.RPCWrite(buffer, resp)
		if err != nil {
			return errors.AddContext(err, "failed to send output to peer")
		}

		instructionSpecifier := program[numOutputs-1].Specifier
		readInstruction := instructionSpecifier == modules.SpecifierReadOffset || instructionSpecifier == modules.SpecifierReadSector
		updateRegistryInstruction := instructionSpecifier == modules.SpecifierUpdateRegistry
		if (readInstruction || updateRegistryInstruction) && h.dependencies.Disrupt("CorruptMDMOutput") {
			// Replace output with same amount of random data.
			fastrand.Read(output.Output)
		}

		// Write output.
		_, err = buffer.Write(output.Output)
		if err != nil {
			return errors.AddContext(err, "failed to send output data to peer")
		}

		// Increase the write deadline just before writing to it.
		err = stream.SetWriteDeadline(time.Now().Add(modules.MDMProgramWriteResponseTime))
		if err != nil {
			return errors.AddContext(err, "failed to set write deadline on stream")
		}

		// Disrupt if the delay write dependency is set
		if h.dependencies.Disrupt("MDMProgramOutputDelayWrite") {
			// add a write delay
			time.Sleep(modules.MDMProgramWriteResponseTime * 2)
		}

		// Disrupt if the slow download dependency is set
		if readInstruction && h.dependencies.Disrupt("SlowDownload") {
			time.Sleep(time.Second)
		}

		// Don't write contents of the buffer if the MDM recommends batching the
		// output as long as the buffer stays under a threshold.
		if output.Batch && buffer.Len() < modules.MDMMaxBatchBufferSize {
			continue
		}

		// Write contents of the buffer.
		_, err = buffer.WriteTo(stream)
		if err != nil {
			return errors.AddContext(err, "failed to send data to peer")
		}
	}

	// Sanity check that we received at least 1 output.
	if numOutputs == 0 {
		err := errors.New("program returned 0 outputs - should never happen")
		build.Critical(err)
		return err
	}

	// Reset the deadline (set both read and write)
	err = stream.SetDeadline(time.Now().Add(defaultConnectionDeadline))
	if err != nil {
		return errors.AddContext(err, "failed to set deadline on stream")
	}

	// If the execution failed we return without an error. The peer will notice
	// the error in the last instruction and know that the communication is over
	// at this point. Nothing more to do than return the promised refund.
	if executionFailed {
		return nil
	}

	// Call finalize if the program is not readonly.
	if !readonly {
		err := h.managedFinalizeWriteProgram(stream, fcid, finalize, output, bh)
		if err != nil {
			return errors.AddContext(err, "failed to finalize write program")
		}
	}
	//	else {
	//		// TODO: finalize spending for readonly programs once the MR is ready. (#4236)
	//	}

	// The program was finalized and we don't want to refund the programRefund
	// anymore.
	programRefund = types.ZeroCurrency
	return nil
}

// managedFinalizeWriteProgram conducts the additional steps required to
// finalize a write program. The blockheight is passed in to make sure we are
// using the same as when we ran the MDMD.
func (h *Host) managedFinalizeWriteProgram(stream io.ReadWriter, fcid types.FileContractID, finalize mdm.FnFinalize, lastOutput mdm.Output, bh types.BlockHeight) error {
	h.mu.Lock()
	sk := h.secretKey
	h.mu.Unlock()

	// Get the storage obligation with write access.
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return errors.AddContext(err, "Failed to get storage obligation for finalizing the program")
	}

	// Get the new revision from the renter.
	var req modules.RPCExecuteProgramRevisionSigningRequest
	err = modules.RPCReadMaxLen(stream, &req, maxRPCExecuteProgramRevisionSigningRequestSize)
	if err != nil {
		return errors.AddContext(err, "failed to get new revision from renter")
	}

	// Construct the new revision.
	currentRevision, err := so.recentRevision()
	if err != nil {
		return errors.AddContext(err, "failed to get current revision")
	}
	transfer := lastOutput.AdditionalCollateral.Add(lastOutput.FailureRefund)
	newRevision, err := currentRevision.ExecuteProgramRevision(req.NewRevisionNumber, transfer, lastOutput.NewMerkleRoot, lastOutput.NewSize)
	if err != nil {
		return errors.AddContext(err, "failed to construct execute program revision")
	}

	// The host is expected to move the additional storage cost and collateral
	// from the missed output to the missed void output.
	maxTransfer := lastOutput.AdditionalCollateral.Add(lastOutput.FailureRefund)

	// Verify the revision.
	err = verifyExecuteProgramRevision(currentRevision, newRevision, bh, maxTransfer, lastOutput.NewSize, lastOutput.NewMerkleRoot)
	if err != nil {
		return errors.AddContext(err, "revision verification failed")
	}

	// Sign the revision.
	renterSig := types.TransactionSignature{
		ParentID:       crypto.Hash(newRevision.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      req.Signature,
	}
	txn, err := createRevisionSignature(newRevision, renterSig, sk, bh)
	if err != nil {
		return errors.AddContext(err, "failed to create signature")
	}

	// Update the storage obligation revision. No need to call
	// `managedModifyStorageObligation since that will be done by the
	// finalize(so) function.
	so.RevisionTransactionSet = []types.Transaction{txn}

	// Send the response to the renter.
	resp := modules.RPCExecuteProgramRevisionSigningResponse{
		Signature: txn.TransactionSignatures[1].Signature,
	}
	err = modules.RPCWrite(stream, resp)
	if err != nil {
		return errors.AddContext(err, "failed to send signature to renter")
	}

	// Finalize the program.
	return errors.AddContext(finalize(so), "program finalizer failed")
}

// verifyExecuteProgramRevision verifies that the new revision is sane in
// relation to the old one.
func verifyExecuteProgramRevision(currentRevision, newRevision types.FileContractRevision, blockHeight types.BlockHeight, maxTransfer types.Currency, newFileSize uint64, newRoot crypto.Hash) error {
	// Check that the revision count has increased.
	if newRevision.NewRevisionNumber <= currentRevision.NewRevisionNumber {
		return ErrBadRevisionNumber
	}

	// Check that the revision is well-formed.
	if len(newRevision.NewValidProofOutputs) != 2 || len(newRevision.NewMissedProofOutputs) != 3 {
		return ErrBadContractOutputCounts
	}

	// Check that the time to finalize and submit the file contract revision
	// has not already passed.
	if currentRevision.NewWindowStart-revisionSubmissionBuffer <= blockHeight {
		return ErrLateRevision
	}

	// Host payout addresses shouldn't change
	if newRevision.ValidHostOutput().UnlockHash != currentRevision.ValidHostOutput().UnlockHash {
		return ErrValidHostOutputAddressChanged
	}
	if newRevision.MissedHostOutput().UnlockHash != currentRevision.MissedHostOutput().UnlockHash {
		return ErrMissedHostOutputAddressChanged
	}

	// Make sure the lost collateral still goes to the void
	missedVoidOutput, err1 := newRevision.MissedVoidOutput()
	existingVoidOutput, err2 := currentRevision.MissedVoidOutput()
	if err := errors.Compose(err1, err2); err != nil {
		return err
	}
	if missedVoidOutput.UnlockHash != existingVoidOutput.UnlockHash {
		return ErrVoidAddressChanged
	}

	// Renter payouts shouldn't change.
	if !currentRevision.ValidRenterPayout().Equals(newRevision.ValidRenterPayout()) {
		return ErrValidRenterPayoutChanged
	}
	if !currentRevision.MissedRenterPayout().Equals(newRevision.MissedRenterPayout()) {
		return ErrMissedRenterPayoutChanged
	}
	// Valid host payout shouldn't change.
	if !currentRevision.ValidHostPayout().Equals(newRevision.ValidHostPayout()) {
		return ErrValidHostPayoutChanged
	}

	// Determine how much money was moved from the host's missed output to the void.
	if newRevision.MissedHostPayout().Cmp(currentRevision.MissedHostPayout()) > 0 {
		return errors.AddContext(ErrLowHostMissedOutput, "host missed proof output was decreased")
	}
	fromHost := currentRevision.MissedHostPayout().Sub(newRevision.MissedHostPayout())
	if fromHost.Cmp(maxTransfer) > 0 {
		return errors.AddContext(ErrLowHostMissedOutput, "more money than expected moved from host")
	}

	// The money subtracted from the host should match the money added to the void.
	mvoOld, errOld := currentRevision.MissedVoidPayout()
	mvoNew, errNew := newRevision.MissedVoidPayout()
	if err := errors.Compose(errOld, errNew); err != nil {
		return errors.AddContext(err, "failed to get missed void payouts")
	}
	if !mvoOld.Add(fromHost).Equals(mvoNew) {
		return errors.New("fromHost doesn't match toVoid")
	}

	// If the renter's valid proof output is larger than the renter's missed
	// proof output, the renter has incentive to see the host fail. Make sure
	// that this incentive is not present.
	if newRevision.ValidRenterPayout().Cmp(newRevision.MissedRenterOutput().Value) > 0 {
		return errors.AddContext(ErrHighRenterMissedOutput, "renter has incentive to see host fail")
	}

	// The filesize should be updated.
	if newRevision.NewFileSize != newFileSize {
		return ErrBadFileSize
	}

	// The merkle root should be updated.
	if newRevision.NewFileMerkleRoot != newRoot {
		return ErrBadFileMerkleRoot
	}

	// Check that all of the non-volatile fields are the same.
	if newRevision.ParentID != currentRevision.ParentID {
		return ErrBadParentID
	}
	if newRevision.UnlockConditions.UnlockHash() != currentRevision.UnlockConditions.UnlockHash() {
		return ErrBadUnlockConditions
	}
	if newRevision.NewWindowStart != currentRevision.NewWindowStart {
		return ErrBadWindowStart
	}
	if newRevision.NewWindowEnd != currentRevision.NewWindowEnd {
		return ErrBadWindowEnd
	}
	if newRevision.NewUnlockHash != currentRevision.NewUnlockHash {
		return ErrBadUnlockHash
	}
	return nil
}
