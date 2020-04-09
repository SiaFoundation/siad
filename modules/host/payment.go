package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// ProcessPayment reads a payment request from the stream. Depending on the type
// of payment it will either update the file contract or call upon the ephemeral
// account manager to process the payment. It will return the account id, the
// amount paid and an error in case of failure. The account id will only be
// valid if the payment method is PayByEphemeralAccount, it will be an empty
// string otherwise.
func (h *Host) ProcessPayment(stream siamux.Stream) (modules.PaymentDetails, error) {
	// read the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return nil, errors.AddContext(err, "Could not read payment request")
	}

	// process payment depending on the payment method
	if pr.Type == modules.PayByEphemeralAccount {
		return h.staticPayByEphemeralAccount(stream)
	}
	if pr.Type == modules.PayByContract {
		return h.managedPayByContract(stream)
	}

	return nil, errors.Compose(fmt.Errorf("Could not handle payment method %v", pr.Type), modules.ErrUnknownPaymentMethod)
}

// staticPayByEphemeralAccount processes a PayByEphemeralAccountRequest coming
// in over the given stream.
func (h *Host) staticPayByEphemeralAccount(stream siamux.Stream) (modules.PaymentDetails, error) {
	// read the PayByEphemeralAccountRequest
	var req modules.PayByEphemeralAccountRequest
	if err := modules.RPCRead(stream, &req); err != nil {
		return nil, errors.AddContext(err, "Could not read PayByEphemeralAccountRequest")
	}

	// get the current account balance.
	accountBalance := h.staticAccountManager.callAccountBalance(req.Message.Account)

	// process the request
	if err := h.staticAccountManager.callWithdraw(&req.Message, req.Signature, req.Priority); err != nil {
		return nil, errors.AddContext(err, "Withdraw failed")
	}

	// send the response
	if err := modules.RPCWrite(stream, modules.PayByEphemeralAccountResponse{Amount: accountBalance}); err != nil {
		return nil, errors.AddContext(err, "Could not send PayByEphemeralAccountResponse")
	}

	// Payment done through EAs don't move collateral
	return newPaymentDetails(req.Message.Account, req.Message.Amount, types.ZeroCurrency), nil
}

// managedPayByContract processes a PayByContractRequest coming in over the
// given stream.
func (h *Host) managedPayByContract(stream siamux.Stream) (modules.PaymentDetails, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return nil, errors.AddContext(err, "Could not read PayByContractRequest")
	}
	fcid := pbcr.ContractID
	accountID := pbcr.RefundAccount

	// lock the storage obligation
	h.managedLockStorageObligation(fcid)
	defer h.managedUnlockStorageObligation(fcid)

	// get the storage obligation
	so, err := h.managedGetStorageObligation(fcid)
	if err != nil {
		return nil, errors.AddContext(err, "Could not fetch storage obligation")
	}

	// get the current blockheight
	bh := h.BlockHeight()

	// extract the proposed revision
	currentRevision, err := so.recentRevision()
	if err != nil {
		return nil, errors.AddContext(err, "Could not find the most recent revision")
	}
	paymentRevision := revisionFromRequest(currentRevision, pbcr)

	// verify the payment revision
	amount, collateral, err := verifyPayByContractRevision(currentRevision, paymentRevision, bh)
	if err != nil {
		return nil, errors.AddContext(err, "Invalid payment revision")
	}

	// sign the revision
	renterSignature := signatureFromRequest(currentRevision, pbcr)
	txn, err := createRevisionSignature(paymentRevision, renterSignature, h.secretKey, h.blockHeight)
	if err != nil {
		return nil, errors.AddContext(err, "Could not create revision signature")
	}

	// get account balance before adding funds.
	// TODO: add managedOpenAccount
	h.staticAccountManager.mu.Lock()
	accBalance := h.staticAccountManager.callAccountBalance(accountID)
	h.staticAccountManager.mu.Unlock()

	// prepare funding the EA.
	syncChan := make(chan struct{})
	err = h.staticAccountManager.callDeposit(accountID, amount, syncChan)
	if err != nil {
		return nil, errors.AddContext(err, "Could not deposit funds in refund account")
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

	// signal that the contract was updated.
	close(syncChan)

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Amount:    accBalance,
		Signature: sig,
	})
	if err != nil {
		return nil, errors.AddContext(err, "Could not send PayByContractResponse")
	}

	return newPaymentDetails(accountID, amount, collateral), nil
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
	amount, _, err := verifyPayByContractRevision(currentRevision, paymentRevision, bh)
	if err != nil {
		return types.ZeroCurrency, errors.AddContext(err, "Invalid payment revision")
	}

	// sign the revision
	renterSignature := signatureFromRequest(currentRevision, pbcr)
	txn, err := createRevisionSignature(paymentRevision, renterSignature, h.secretKey, h.blockHeight)
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
	return types.TransactionSignature{
		ParentID:       crypto.Hash(recent.ParentID),
		CoveredFields:  types.CoveredFields{FileContractRevisions: []uint64{0}},
		PublicKeyIndex: 0,
		Signature:      pbcr.Signature,
	}
}

// verifyPayByContractRevision verifies the given payment revision and returns
// the amount that was transferred, the collateral that was moved and a
// potential error.
func verifyPayByContractRevision(current, payment types.FileContractRevision, blockHeight types.BlockHeight) (amount, collateral types.Currency, err error) {
	if err = verifyPaymentRevision(current, payment, blockHeight, types.ZeroCurrency); err != nil {
		return
	}

	// Note that we can safely subtract the values of the outputs seeing as verifyPaymentRevision will have checked for potential underflows
	amount = payment.ValidHostPayout().Sub(current.ValidHostPayout())
	collateral = current.MissedHostOutput().Value.Sub(payment.MissedHostOutput().Value)
	return
}

// payment details is a helper struct that implements the PaymentDetails
// interface.
type paymentDetails struct {
	account         modules.AccountID
	amount          types.Currency
	addedCollateral types.Currency
}

// newPaymentDetails returns a new paymentDetails object using the given values
func newPaymentDetails(account modules.AccountID, amountPaid, addedCollateral types.Currency) *paymentDetails {
	return &paymentDetails{
		account:         account,
		amount:          amountPaid,
		addedCollateral: addedCollateral,
	}
}

// AccountID returns the account id used for payment. For payments made by
// contract this will return the empty string.
func (pd *paymentDetails) AccountID() modules.AccountID { return pd.account }

// Amount returns how much money the host received.
func (pd *paymentDetails) Amount() types.Currency { return pd.amount }

// AddedCollatoral returns the amount of collateral that moved from the host to
// the void output.
func (pd *paymentDetails) AddedCollateral() types.Currency { return pd.addedCollateral }
