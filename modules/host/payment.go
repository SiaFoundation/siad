package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// ProcessPayment reads a payment request from the stream, depending on the type
// of payment it will either update the file contract or call upon the ephemeral
// account manager to process the payment.
func (h *Host) ProcessPayment(stream siamux.Stream) (types.Currency, error) {
	// read the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not read payment request")
	}
	// process payment depending on the payment method
	switch pr.Type {
	case modules.PayByEphemeralAccount:
		return h.staticPayByEphemeralAccount(stream)
	case modules.PayByContract:
		return h.managedPayByContract(stream)
	default:
		return types.ZeroCurrency, errors.Compose(fmt.Errorf("Could not handle payment method %v", pr.Type), modules.ErrUnknownPaymentMethod)
	}
}

// staticPayByEphemeralAccount processes a PayByEphemeralAccountRequest coming
// in over the given stream.
func (h *Host) staticPayByEphemeralAccount(stream siamux.Stream) (types.Currency, error) {
	// read the PayByEphemeralAccountRequest
	var pbear modules.PayByEphemeralAccountRequest
	if err := modules.RPCRead(stream, &pbear); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not read PayByEphemeralAccountRequest")
	}

	// process the request
	if err := h.staticAccountManager.callWithdraw(&pbear.Message, pbear.Signature, pbear.Priority); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Withdraw failed")
	}

	// send the response
	if err := modules.RPCWrite(stream, modules.PayByEphemeralAccountResponse{Amount: pbear.Message.Amount}); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not send PayByEphemeralAccountResponse")
	}

	return pbear.Message.Amount, nil
}

// managedPayByContract processes a PayByContractRequest coming in over the
// given stream.
func (h *Host) managedPayByContract(stream siamux.Stream) (types.Currency, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not read PayByContractRequest")
	}
	fcid := pbcr.ContractID

	// lock the storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// get the storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not fetch storage obligation")
	}

	// extract the proposed revision and the signature from the request
	recentRevision := so.recentRevision()
	renterRevision := revisionFromRequest(recentRevision, pbcr)
	renterSignature := signatureFromRequest(recentRevision, pbcr)

	// sign the revision
	txn, err := createRevisionSignature(renterRevision, renterSignature, h.secretKey, h.blockHeight)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not create revision signature")
	}

	// extract the payment output & update the storage obligation with the
	// host's signature
	amount := recentRevision.NewValidProofOutputs[0].Value.Sub(renterRevision.NewValidProofOutputs[0].Value)
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{renterRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// update the storage obligation
	err = h.managedModifyStorageObligation(so, nil, nil)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not modify storage obligation")
	}

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	if err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	}); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not send PayByContractResponse")
	}

	return amount, nil
}

// managedFundByContract processes a PayByContractRequest coming in over the
// given stream, intended to pay for the given FundAccountRequest. Note that
// this method is very similar to managedPayByContract, however it has to be
// separate due to the orchestration required to both fund the ephemeral account
// and fsync the storage obligation to disk. See `callDeposit` for more details.
func (h *Host) managedFundByContract(stream siamux.Stream, request modules.FundAccountRequest, cost types.Currency) (types.Currency, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not read PayByContractRequest")
	}
	fcid := pbcr.ContractID

	// lock the storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// get the storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not fetch storage obligation")
	}

	// extract the proposed revision and the signature from the request
	recentRevision := so.recentRevision()
	renterRevision := revisionFromRequest(recentRevision, pbcr)
	renterSignature := signatureFromRequest(recentRevision, pbcr)

	// sign the revision
	txn, err := createRevisionSignature(renterRevision, renterSignature, h.secretKey, h.blockHeight)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not create revision signature")
	}

	// extract the payment
	amount := recentRevision.NewValidProofOutputs[0].Value.Sub(renterRevision.NewValidProofOutputs[0].Value)
	if amount.Cmp(cost) < 0 {
		return types.ZeroCurrency, errors.New("Could not fund, deposit was zero after deducting the cost")
	}
	deposit := amount.Sub(cost)

	// create a sync chan to pass to the account manager, once the FC is fully
	// fsynced we'll close this so the account manager can properly lower the
	// host's outstanding risk induced by the (immediate) deposit.
	syncChan := make(chan struct{})
	if err = h.staticAccountManager.callDeposit(request.AccountID, deposit, syncChan); err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not deposit funds")
	}

	// update the storage obligation with the host's signature
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{renterRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// update the storage obligation
	err = h.managedModifyStorageObligation(so, nil, nil)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Could not modify storage obligation")
	}
	close(syncChan) // signal FC fsync by closing the sync channel

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	if err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	}); err != nil {
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
		rev.NewValidProofOutputs[i] = types.SiacoinOutput{
			Value:      v,
			UnlockHash: recent.NewValidProofOutputs[i].UnlockHash,
		}
	}

	rev.NewMissedProofOutputs = make([]types.SiacoinOutput, len(pbcr.NewMissedProofValues))
	for i, v := range pbcr.NewMissedProofValues {
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
	txn := types.NewTransaction(recent, 0)
	txn.TransactionSignatures[0].Signature = pbcr.Signature
	return txn.TransactionSignatures[0]
}
