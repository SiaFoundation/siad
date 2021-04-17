package host

import (
	"fmt"
	"net"
	"time"

	"gitlab.com/NebulousLabs/encoding"
	"gitlab.com/NebulousLabs/errors"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

var (
	// errLargeDownloadBatch is returned if the renter requests a download
	// batch that exceeds the maximum batch size that the host will
	// accommodate.
	errLargeDownloadBatch = ErrorCommunication("download request exceeded maximum batch size")

	// errRequestOutOfBounds is returned when a download request is made which
	// asks for elements of a sector which do not exist.
	errRequestOutOfBounds = ErrorCommunication("download request has invalid sector bounds")
)

// managedDownloadIteration is responsible for managing a single iteration of
// the download loop for RPCDownload.
func (h *Host) managedDownloadIteration(conn net.Conn, so *storageObligation) error {
	// Exchange settings with the renter.
	err := h.managedRPCSettings(conn)
	if err != nil {
		return extendErr("RPCSettings failed: ", err)
	}

	// Extend the deadline for the download.
	conn.SetDeadline(time.Now().Add(modules.NegotiateDownloadTime))

	// The renter will either accept or reject the host's settings.
	err = modules.ReadNegotiationAcceptance(conn)
	if errors.Contains(err, modules.ErrStopResponse) {
		return err // managedRPCDownload will catch this and exit gracefully
	} else if err != nil {
		return extendErr("renter rejected host settings: ", ErrorCommunication(err.Error()))
	}

	// Grab a set of variables that will be useful later in the function.
	_, maxFee := h.tpool.FeeEstimation()
	h.mu.Lock()
	blockHeight := h.blockHeight
	secretKey := h.secretKey
	settings := h.externalSettings(maxFee)
	h.mu.Unlock()

	// Read the download requests, followed by the file contract revision that
	// pays for them.
	var requests []modules.DownloadAction
	var paymentRevision types.FileContractRevision
	err = encoding.ReadObject(conn, &requests, modules.NegotiateMaxDownloadActionRequestSize)
	if err != nil {
		return extendErr("failed to read download requests:", ErrorConnection(err.Error()))
	}
	err = encoding.ReadObject(conn, &paymentRevision, modules.NegotiateMaxFileContractRevisionSize)
	if err != nil {
		return extendErr("failed to read payment revision:", ErrorConnection(err.Error()))
	}

	// Verify that the request is acceptable, and then fetch all of the data
	// for the renter.
	existingRevision := so.RevisionTransactionSet[len(so.RevisionTransactionSet)-1].FileContractRevisions[0]
	var payload [][]byte
	err = func() error {
		// Check that the length of each file is in-bounds, and that the total
		// size being requested is acceptable.
		var totalSize uint64
		for _, request := range requests {
			if request.Length > modules.SectorSize || request.Offset+request.Length > modules.SectorSize {
				return extendErr("download iteration request failed: ", errRequestOutOfBounds)
			}
			totalSize += request.Length
		}
		if totalSize > settings.MaxDownloadBatchSize {
			return extendErr("download iteration batch failed: ", errLargeDownloadBatch)
		}

		// Verify that the correct amount of money has been moved from the
		// renter's contract funds to the host's contract funds.
		expectedTransfer := settings.DownloadBandwidthPrice.Mul64(totalSize)
		err = verifyPaymentRevision(existingRevision, paymentRevision, blockHeight, expectedTransfer)
		if err != nil {
			return extendErr("payment verification failed: ", err)
		}

		// Load the sectors and build the data payload.
		for _, request := range requests {
			sectorData, err := h.ReadSector(request.MerkleRoot)
			if err != nil {
				return extendErr("failed to load sector: ", ErrorInternal(err.Error()))
			}
			payload = append(payload, sectorData[request.Offset:request.Offset+request.Length])
		}
		return nil
	}()
	if err != nil {
		modules.WriteNegotiationRejection(conn, err) // Error not reported to preserve type in extendErr
		return extendErr("download request rejected: ", err)
	}
	// Revision is acceptable, write acceptance.
	err = modules.WriteNegotiationAcceptance(conn)
	if err != nil {
		return extendErr("failed to write acceptance for renter revision: ", ErrorConnection(err.Error()))
	}

	// Renter will send a transaction signature for the file contract revision.
	var renterSignature types.TransactionSignature
	err = encoding.ReadObject(conn, &renterSignature, modules.NegotiateMaxTransactionSignatureSize)
	if err != nil {
		return extendErr("failed to read renter signature: ", ErrorConnection(err.Error()))
	}
	txn, err := createRevisionSignature(paymentRevision, renterSignature, secretKey, blockHeight)

	// Existing revisions's renter payout can't be smaller than the payment
	// revision's since that would cause an underflow.
	if existingRevision.ValidRenterPayout().Cmp(paymentRevision.ValidRenterPayout()) < 0 {
		return errors.New("existing revision's renter payout is smaller than the payment revision's")
	}
	// Update the storage obligation.
	paymentTransfer := existingRevision.ValidRenterPayout().Sub(paymentRevision.ValidRenterPayout())
	so.PotentialDownloadRevenue = so.PotentialDownloadRevenue.Add(paymentTransfer)
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{paymentRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}
	err = h.managedModifyStorageObligation(*so, nil, nil)
	if err != nil {
		return extendErr("failed to modify storage obligation: ", ErrorInternal(modules.WriteNegotiationRejection(conn, err).Error()))
	}

	// Write acceptance to the renter - the data request can be fulfilled by
	// the host, the payment is satisfactory, signature is correct. Then send
	// the host signature and all of the data.
	err = modules.WriteNegotiationAcceptance(conn)
	if err != nil {
		return extendErr("failed to write acceptance following obligation modification: ", ErrorConnection(err.Error()))
	}
	err = encoding.WriteObject(conn, txn.TransactionSignatures[1])
	if err != nil {
		return extendErr("failed to write signature: ", ErrorConnection(err.Error()))
	}
	err = encoding.WriteObject(conn, payload)
	if err != nil {
		return extendErr("failed to write payload: ", ErrorConnection(err.Error()))
	}
	return nil
}

// verifyPaymentRevision verifies that the revision being provided to pay for
// the data has transferred the expected amount of money from the renter to the
// host.
func verifyPaymentRevision(existingRevision, paymentRevision types.FileContractRevision, blockHeight types.BlockHeight, expectedTransfer types.Currency) error {
	// Check that the revision count has increased.
	if paymentRevision.NewRevisionNumber <= existingRevision.NewRevisionNumber {
		return ErrBadRevisionNumber
	}

	// Check that the revision is well-formed.
	if len(paymentRevision.NewValidProofOutputs) != 2 || len(paymentRevision.NewMissedProofOutputs) != 3 {
		return ErrBadContractOutputCounts
	}

	// Check that the time to finalize and submit the file contract revision
	// has not already passed.
	if existingRevision.NewWindowStart-revisionSubmissionBuffer <= blockHeight {
		return ErrLateRevision
	}

	// Host payout addresses shouldn't change
	if paymentRevision.ValidHostOutput().UnlockHash != existingRevision.ValidHostOutput().UnlockHash {
		return errors.New("host payout address changed")
	}
	if paymentRevision.MissedHostOutput().UnlockHash != existingRevision.MissedHostOutput().UnlockHash {
		return errors.New("host payout address changed")
	}
	// Make sure the lost collateral still goes to the void
	paymentVoidOutput, err1 := paymentRevision.MissedVoidOutput()
	existingVoidOutput, err2 := existingRevision.MissedVoidOutput()
	if err := errors.Compose(err1, err2); err != nil {
		return err
	}
	if paymentVoidOutput.UnlockHash != existingVoidOutput.UnlockHash {
		return ErrVoidAddressChanged
	}

	// Determine the amount that was transferred from the renter.
	if paymentRevision.ValidRenterPayout().Cmp(existingRevision.ValidRenterPayout()) > 0 {
		return errors.AddContext(ErrHighRenterValidOutput, "renter increased its valid proof output")
	}
	fromRenter := existingRevision.ValidRenterPayout().Sub(paymentRevision.ValidRenterPayout())
	// Verify that enough money was transferred.
	if fromRenter.Cmp(expectedTransfer) < 0 {
		s := fmt.Sprintf("expected at least %v to be exchanged, but %v was exchanged: ", expectedTransfer, fromRenter)
		return errors.AddContext(ErrHighRenterValidOutput, s)
	}

	// Determine the amount of money that was transferred to the host.
	if existingRevision.ValidHostPayout().Cmp(paymentRevision.ValidHostPayout()) > 0 {
		return errors.AddContext(ErrLowHostValidOutput, "host valid proof output was decreased")
	}
	toHost := paymentRevision.ValidHostPayout().Sub(existingRevision.ValidHostPayout())
	// Verify that enough money was transferred.
	if !toHost.Equals(fromRenter) {
		s := fmt.Sprintf("expected exactly %v to be transferred to the host, but %v was transferred: ", fromRenter, toHost)
		return errors.AddContext(ErrLowHostValidOutput, s)
	}
	// The amount of money moved to the void output should match the money moved
	// to the valid host output.
	if !paymentVoidOutput.Value.Equals(existingVoidOutput.Value.Add(toHost)) {
		return ErrLowVoidOutput
	}

	// If the renter's valid proof output is larger than the renter's missed
	// proof output, the renter has incentive to see the host fail. Make sure
	// that this incentive is not present.
	if paymentRevision.ValidRenterPayout().Cmp(paymentRevision.MissedRenterOutput().Value) > 0 {
		return errors.AddContext(ErrHighRenterMissedOutput, "renter has incentive to see host fail")
	}

	// Check that the host is not going to be posting collateral.
	if paymentRevision.MissedHostOutput().Value.Cmp(existingRevision.MissedHostOutput().Value) < 0 {
		collateral := existingRevision.MissedHostOutput().Value.Sub(paymentRevision.MissedHostOutput().Value)
		s := fmt.Sprintf("host not expecting to post any collateral, but contract has host posting %v collateral", collateral)
		return errors.AddContext(ErrLowHostMissedOutput, s)
	}

	// Check that all of the non-volatile fields are the same.
	if paymentRevision.ParentID != existingRevision.ParentID {
		return ErrBadParentID
	}
	if paymentRevision.UnlockConditions.UnlockHash() != existingRevision.UnlockConditions.UnlockHash() {
		return ErrBadUnlockConditions
	}
	if paymentRevision.NewFileSize != existingRevision.NewFileSize {
		return ErrBadFileSize
	}
	if paymentRevision.NewFileMerkleRoot != existingRevision.NewFileMerkleRoot {
		return ErrBadFileMerkleRoot
	}
	if paymentRevision.NewWindowStart != existingRevision.NewWindowStart {
		return ErrBadWindowStart
	}
	if paymentRevision.NewWindowEnd != existingRevision.NewWindowEnd {
		return ErrBadWindowEnd
	}
	if paymentRevision.NewUnlockHash != existingRevision.NewUnlockHash {
		return ErrBadUnlockHash
	}
	if !paymentRevision.MissedHostOutput().Value.Equals(existingRevision.MissedHostOutput().Value) {
		return ErrLowHostMissedOutput
	}
	if err := verifyPayoutSums(existingRevision, paymentRevision); err != nil {
		return errors.Compose(ErrInvalidPayoutSums, err)
	}
	return nil
}

// verifyPayoutSums compares two revisions and makes sure Sum(validPayouts,
// missedPayouts) is true for both and that they both have the same payout.
func verifyPayoutSums(oldRevision, newRevision types.FileContractRevision) error {
	oldValid, oldMissed := oldRevision.TotalPayout()
	if !oldValid.Equals(oldMissed) {
		return errors.New("old revision's valid output doesn't match missed output")
	}
	newValid, newMissed := newRevision.TotalPayout()
	if !newValid.Equals(newMissed) {
		return errors.New("new revision's valid output doesn't match missed output")
	}
	if !oldValid.Equals(newValid) {
		return errors.New("total payout of new revision doesn't match old revision's")
	}
	return nil
}

// managedRPCDownload is responsible for handling an RPC request from the
// renter to download data.
func (h *Host) managedRPCDownload(conn net.Conn) error {
	// Get the start time to limit the length of the whole connection.
	startTime := time.Now()
	// Perform the file contract revision exchange, giving the renter the most
	// recent file contract revision and getting the storage obligation that
	// will be used to pay for the data.
	_, so, err := h.managedRPCRecentRevision(conn)
	if err != nil {
		return extendErr("failed RPCRecentRevision during RPCDownload: ", err)
	}
	// The storage obligation is returned with a lock on it. Defer a call to
	// unlock the storage obligation.
	defer func() {
		h.managedUnlockStorageObligation(so.id())
	}()

	// Perform a loop that will allow downloads to happen until the maximum
	// time for a single connection has been reached.
	for time.Now().Before(startTime.Add(iteratedConnectionTime)) {
		err := h.managedDownloadIteration(conn, &so)
		if errors.Contains(err, modules.ErrStopResponse) {
			// The renter has indicated that it has finished downloading the
			// data, therefore there is no error. Return nil.
			return nil
		} else if err != nil {
			return extendErr("download iteration failed: ", err)
		}
	}
	return nil
}
