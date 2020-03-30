package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/build"
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

	// process the request
	if err := h.staticAccountManager.callWithdraw(&req.Message, req.Signature, req.Priority); err != nil {
		return nil, errors.AddContext(err, "Withdraw failed")
	}

	// send the response
	if err := modules.RPCWrite(stream, modules.PayByEphemeralAccountResponse{Amount: req.Message.Amount}); err != nil {
		return nil, errors.AddContext(err, "Could not send PayByEphemeralAccountResponse")
	}

	return accountPaymentDetails(req.Message)
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

	// extract the amount the renter is paying the host
	crp := currentRevision.ValidRenterPayout()
	nrp := paymentRevision.ValidRenterPayout()
	if crp.Cmp(nrp) < 0 {
		return nil, errors.AddContext(err, "Invalid renter payout, causes underflow")
	}
	amount := crp.Sub(nrp)

	// verify the payment revision
	err = verifyPaymentRevision(currentRevision, paymentRevision, bh, amount)
	if err != nil {
		return nil, errors.AddContext(err, "Invalid payment revision")
	}

	// sign the revision
	renterSignature := signatureFromRequest(currentRevision, pbcr)
	txn, err := createRevisionSignature(paymentRevision, renterSignature, h.secretKey, h.blockHeight)
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

	return contractPaymentDetails(currentRevision, paymentRevision)
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

// payment details is a helper struct that implements the PaymentDetails
// interface.
type paymentDetails struct {
	account         string
	amount          types.Currency
	addedCollateral types.Currency
}

// accountPaymentDetails returns the payment details for a payment that was
// paid for using an ephemeral account.
func accountPaymentDetails(message modules.WithdrawalMessage) (*paymentDetails, error) {
	return &paymentDetails{
		account: message.Account,
		amount:  message.Amount,
	}, nil
}

// contractPaymentDetails returns the payment details for a payment that was
// paid for using a file contract. It takes the old and new file contract
// revision to calculate the amount that was paid to the host and how much
// collateral moved.
//
// Note that this function does not validate the revisions. To mitigate possible
// underflows the values are calculated using safeSub which performs a sanity
// check against underflows. The payment revision will have been verified during
// the payment process.
func contractPaymentDetails(curr, payment types.FileContractRevision) (*paymentDetails, error) {
	vhp, err := safeSub(payment.ValidHostPayout(), curr.ValidHostPayout())
	if err != nil {
		return nil, err
	}
	mho, err := safeSub(curr.MissedHostOutput().Value, payment.MissedHostOutput().Value)
	if err != nil {
		return nil, err
	}
	return &paymentDetails{
		amount:          vhp,
		addedCollateral: mho,
	}, nil
}

// Account returns the account id used for payment. For payments made by
// contract this will return the empty string.
func (pd *paymentDetails) Account() string { return pd.account }

// Amount returns how much money the host received.
func (pd *paymentDetails) Amount() types.Currency { return pd.amount }

// AddedCollatoral returns the amount of collateral that moved from the host to
// the void output.
func (pd *paymentDetails) AddedCollateral() types.Currency { return pd.addedCollateral }

// safeSub is a helper function that subtracts but at the same time performs a
// sanity check to harden against underflows.
func safeSub(x, y types.Currency) (types.Currency, error) {
	if x.Cmp(y) < 0 {
		err := fmt.Errorf("Subtract would causes underflow")
		build.Critical(err.Error())
		return types.ZeroCurrency, err
	}
	return x.Sub(y), nil
}
