package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/modules"
	"go.sia.tech/siad/types"
)

// ProcessPayment reads a payment request from the stream. Depending on the type
// of payment it will either update the file contract or call upon the ephemeral
// account manager to process the payment. It will return the account id, the
// amount paid and an error in case of failure. The account id will only be
// valid if the payment method is PayByEphemeralAccount, it will be an empty
// string otherwise.
func (h *Host) ProcessPayment(stream siamux.Stream, bh types.BlockHeight) (modules.PaymentDetails, error) {
	// read the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return nil, errors.AddContext(err, "Could not read payment request")
	}

	// process payment depending on the payment method
	if pr.Type == modules.PayByEphemeralAccount {
		return h.staticPayByEphemeralAccount(stream, bh)
	}
	if pr.Type == modules.PayByContract {
		return h.managedPayByContract(stream, bh)
	}

	return nil, errors.Compose(fmt.Errorf("Could not handle payment method %v", pr.Type), modules.ErrUnknownPaymentMethod)
}

// staticPayByEphemeralAccount processes a PayByEphemeralAccountRequest coming
// in over the given stream.
func (h *Host) staticPayByEphemeralAccount(stream siamux.Stream, bh types.BlockHeight) (modules.PaymentDetails, error) {
	// read the PayByEphemeralAccountRequest
	var req modules.PayByEphemeralAccountRequest
	if err := modules.RPCRead(stream, &req); err != nil {
		return nil, errors.AddContext(err, "Could not read PayByEphemeralAccountRequest")
	}

	// process the request
	if err := h.staticAccountManager.callWithdraw(&req.Message, req.Signature, req.Priority, bh); err != nil {
		return nil, errors.AddContext(err, "Withdraw failed")
	}

	// Payment done through EAs don't move collateral
	return newPaymentDetails(req.Message.Account, req.Message.Amount), nil
}

// managedPayByContract processes a PayByContractRequest coming in over the
// given stream.
func (h *Host) managedPayByContract(stream siamux.Stream, bh types.BlockHeight) (modules.PaymentDetails, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return nil, errors.AddContext(err, "Could not read PayByContractRequest")
	}
	fcid := pbcr.ContractID
	accountID := pbcr.RefundAccount

	// sanity check accountID. Should always be provided.
	if accountID.IsZeroAccount() {
		return nil, errors.New("no account id provided for refunds")
	}

	// lock the storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// simulate a missing obligation.
	if h.dependencies.Disrupt("StorageObligationNotFound") {
		return nil, errors.AddContext(errNoStorageObligation, "Could not fetch storage obligation")
	}

	// get the storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return nil, errors.AddContext(err, "Could not fetch storage obligation")
	}

	// get the current blockheight
	h.mu.RLock()
	sk := h.secretKey
	h.mu.RUnlock()

	// extract the proposed revision
	currentRevision, err := so.recentRevision()
	if err != nil {
		return nil, errors.AddContext(err, "Could not find the most recent revision")
	}
	paymentRevision := revisionFromRequest(currentRevision, pbcr)

	// verify the payment revision
	amount, err := verifyPayByContractRevision(currentRevision, paymentRevision, bh)
	if err != nil {
		return nil, errors.AddContext(err, "Invalid payment revision")
	}

	// sign the revision
	renterSignature := signatureFromRequest(currentRevision, pbcr)
	txn, err := createRevisionSignature(paymentRevision, renterSignature, sk, bh)
	if err != nil {
		return nil, errors.AddContext(err, "Could not create revision signature")
	}

	// extract the payment output & update the storage obligation with the
	// host's signature
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{paymentRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// update the storage obligation
	err = h.managedModifyStorageObligation(so, nil, nil)
	if err != nil {
		return nil, errors.AddContext(err, "Could not modify storage obligation")
	}

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	})
	if err != nil {
		return nil, errors.AddContext(err, "Could not send PayByContractResponse")
	}

	return newPaymentDetails(accountID, amount), nil
}

// managedFundAccount processes a PayByContractRequest coming in over the given
// stream, intended to pay for the given FundAccountRequest. Note that this
// method is very similar to managedPayByContract, however it has to be separate
// due to the orchestration required to both fund the ephemeral account and
// fsync the storage obligation to disk. See `callDeposit` for more details.
func (h *Host) managedFundAccount(stream siamux.Stream, request modules.FundAccountRequest, cost types.Currency) (types.Currency, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not read PayByContractRequest")
	}
	fcid := pbcr.ContractID

	// can't provide a refund address when funding an account.
	if !pbcr.RefundAccount.IsZeroAccount() {
		return types.ZeroCurrency, errors.New("can't provide a refund account on a fund account rpc")
	}

	// lock the storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// get the storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not fetch storage obligation")
	}

	// get the current blockheight
	bh := h.BlockHeight()

	// extract the proposed revision
	currentRevision, err := so.recentRevision()
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not get the latest revision")
	}
	paymentRevision := revisionFromRequest(currentRevision, pbcr)

	// verify the payment revision
	amount, err := verifyPayByContractRevision(currentRevision, paymentRevision, bh)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Invalid payment revision")
	}

	// sign the revision
	renterSignature := signatureFromRequest(currentRevision, pbcr)
	txn, err := createRevisionSignature(paymentRevision, renterSignature, h.secretKey, bh)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not create revision signature")
	}

	// copy the transaction signature
	var sig crypto.Signature
	if len(txn.HostSignature().Signature) != len(sig) {
		return types.ZeroCurrency, errors.AddContext(err, fmt.Sprintf("Invalid transaction signature, expected a crypto.Signature but received a signature with length %v", len(txn.HostSignature().Signature)))
	}
	copy(sig[:], txn.HostSignature().Signature[:])

	// extract the payment
	if amount.Cmp(cost) < 0 {
		return types.ZeroCurrency, errors.New("Could not fund, the amount that was deposited did not cover the cost of the RPC")
	}
	deposit := amount.Sub(cost)

	// create a sync chan to pass to the account manager, once the FC is fully
	// fsynced we'll close this so the account manager can properly lower the
	// host's outstanding risk induced by the (immediate) deposit.
	syncChan := make(chan struct{})
	err = h.staticAccountManager.callDeposit(request.Account, deposit, syncChan)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not deposit funds")
	}

	// update the storage obligation with the host's signature
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{paymentRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// track the account funding
	so.PotentialAccountFunding = so.PotentialAccountFunding.Add(deposit)

	// update the storage obligation
	err = h.managedModifyStorageObligation(so, nil, nil)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not modify storage obligation")
	}
	close(syncChan) // signal FC fsync by closing the sync channel

	// send the response
	err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	})
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not send PayByContractResponse")
	}

	return deposit, nil
}

// revisionFromRequest is a helper function that creates a copy of the recent
// revision and decorates it with the suggested revision values which are
// provided through the PayByContractRequest object.
func revisionFromRequest(recent types.FileContractRevision, pbcr modules.PayByContractRequest) types.FileContractRevision {
	rev := recent

	rev.NewRevisionNumber = pbcr.NewRevisionNumber
	rev.NewValidProofOutputs = make([]types.SiacoinOutput, len(pbcr.NewValidProofValues))
	for i, v := range pbcr.NewValidProofValues {
		if i >= len(recent.NewValidProofOutputs) {
			break
		}
		rev.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      v,
			UnlockHash: recent.NewValidProofOutputs[i].UnlockHash,
		}
	}

	rev.NewMissedProofOutputs = make([]types.SiacoinOutput, len(pbcr.NewMissedProofValues))
	for i, v := range pbcr.NewMissedProofValues {
		if i >= len(recent.NewMissedProofOutputs) {
			break
		}
		rev.NewMissedProofOutputs[i] = types.SiacoinOutput{
			Value:      v,
			UnlockHash: recent.NewMissedProofOutputs[i].UnlockHash,
		}
	}

	return rev
}

// signatureFromRequest is a helper function that creates a copy of the recent
// revision and decorates it with the signature provided through the
// PayByContractRequest object.
func signatureFromRequest(recent types.FileContractRevision, pbcr modules.PayByContractRequest) types.TransactionSignature {
	return types.TransactionSignature{
		ParentID:       crypto.Hash(recent.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      pbcr.Signature,
	}
}

// verifyEAFundRevision verifies that the revision being provided to pay for
// the data has transferred the expected amount of money from the renter to the
// host.
func verifyEAFundRevision(existingRevision, paymentRevision types.FileContractRevision, blockHeight types.BlockHeight, expectedTransfer types.Currency) error {
	// Check that the revision count has increased.
	if paymentRevision.NewRevisionNumber <= existingRevision.NewRevisionNumber {
		return errors.AddContext(ErrBadRevisionNumber, fmt.Sprintf("%v <= %v", paymentRevision.NewRevisionNumber, existingRevision.NewRevisionNumber))
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
		return errors.New("lost collateral address was changed")
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
	// The amount of money moved to the missing host output should match the
	// money moved to the valid output.
	if !paymentRevision.MissedHostPayout().Equals(existingRevision.MissedHostPayout().Add(toHost)) {
		return ErrLowHostMissedOutput
	}

	// If the renter's valid proof output is larger than the renter's missed
	// proof output, the renter has incentive to see the host fail. Make sure
	// that this incentive is not present.
	if paymentRevision.ValidRenterPayout().Cmp(paymentRevision.MissedRenterOutput().Value) > 0 {
		return errors.AddContext(ErrHighRenterMissedOutput, "renter has incentive to see host fail")
	}

	// Check that the host is not going to be posting collateral.
	if !existingVoidOutput.Value.Equals(paymentVoidOutput.Value) {
		s := fmt.Sprintf("void payout wasn't expected to change")
		return errors.AddContext(ErrVoidPayoutChanged, s)
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
	if err := verifyPayoutSums(existingRevision, paymentRevision); err != nil {
		return errors.Compose(ErrInvalidPayoutSums, err)
	}
	return nil
}

// verifyPayByContractRevision verifies the given payment revision and returns
// the amount that was transferred, the collateral that was moved and a
// potential error.
func verifyPayByContractRevision(current, payment types.FileContractRevision, blockHeight types.BlockHeight) (amount types.Currency, err error) {
	if err = verifyEAFundRevision(current, payment, blockHeight, types.ZeroCurrency); err != nil {
		return
	}

	// Note that we can safely subtract the values of the outputs seeing as verifyPaymentRevision will have checked for potential underflows
	amount = payment.ValidHostPayout().Sub(current.ValidHostPayout())
	return
}

// payment details is a helper struct that implements the PaymentDetails
// interface.
type paymentDetails struct {
	account modules.AccountID
	amount  types.Currency
}

// newPaymentDetails returns a new paymentDetails object using the given values
func newPaymentDetails(account modules.AccountID, amountPaid types.Currency) *paymentDetails {
	return &paymentDetails{
		account: account,
		amount:  amountPaid,
	}
}

// AccountID returns the account id used for payment. For payments made by
// contract this will return the empty string.
func (pd *paymentDetails) AccountID() modules.AccountID { return pd.account }

// Amount returns how much money the host received.
func (pd *paymentDetails) Amount() types.Currency { return pd.amount }
