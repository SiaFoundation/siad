package host

import (
	"fmt"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// TODO: the PayByEphemeralAccountResponse is redundant and is not used by the
// renter in any way, it contains the exact some amount that the renter has
// sent, perhaps we can drop it entirely.

// paymentProcessor fulfills the PaymentProcessor interface on the host. It is
// used by the RPCs to process a payment that is sent over a stream.
type paymentProcessor struct {
	staticSecretKey crypto.SecretKey
	h               *Host
}

// NewPaymentProcessor returns a new PaymentProcessor.
func (h *Host) NewPaymentProcessor() modules.PaymentProcessor {
	return &paymentProcessor{
		staticSecretKey: h.secretKey,
		h:               h,
	}
}

// ProcessPaymentForRPC reads a payment request from the stream, depending on
// the type of payment it will either update the file contract or call upon the
// ephemeral account manager to process the payment.
func (p *paymentProcessor) ProcessPaymentForRPC(stream siamux.Stream) (types.Currency, error) {
	// read the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return failTo("ProcessPaymentForRPC", "read PaymentRequest", err)
	}
	// process payment depending on the payment method
	switch pr.Type {
	case modules.PayByEphemeralAccount:
		return p.payByEphemeralAccount(stream)
	case modules.PayByContract:
		return p.payByContract(stream)
	default:
		return failTo("ProcessPaymentForRPC", "handle payment method", modules.ErrUnknownPaymentMethod)
	}
}

// ProcessFundEphemeralAccountRPC reads a payment request from the stream with
// the intention of funding an ephemeral account. This is treated as a special
// case because it requires some coordination between the FC fsync and the EA
// fsync. See callDeposit in accountmanager.go for more details.
func (p *paymentProcessor) ProcessFundEphemeralAccountRPC(stream siamux.Stream, pt modules.RPCPriceTable, accountID string) (types.Currency, error) {
	// read the PaymentRequest
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return failTo("ProcessFundEphemeralAccountRPC", "read PaymentRequest", err)
	}

	// ensure it's a PayByContract request
	if pr.Type != modules.PayByContract {
		return failTo("ProcessFundEphemeralAccountRPC", "handle payment method", modules.ErrInvalidPaymentMethod)
	}

	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return failTo("ProcessFundEphemeralAccountRPC", "read PayByContractRequest", err)
	}

	// lock the storage obligation
	so, err := p.lockStorageObligation(pbcr.ContractID)
	defer p.h.managedUnlockStorageObligation(so.id())
	if err != nil {
		return failTo("ProcessFundEphemeralAccountRPC", "lock storage obligation", err)
	}

	// extract the proposed revision and the signature from the request
	recentRevision := so.recentRevision()
	renterRevision := revisionFromRequest(recentRevision, pbcr)
	renterSignature := signatureFromRequest(recentRevision, pbcr)

	// sign the revision
	blockHeight := p.h.BlockHeight()
	txn, err := createRevisionSignature(renterRevision, renterSignature, p.staticSecretKey, blockHeight)
	if err != nil {
		return failTo("ProcessFundEphemeralAccountRPC", "verify revision", err)
	}

	// calculate the deposit amount, this equals to amount of money paid minus
	// the cost of the RPC
	cost := pt.FundEphemeralAccountCost
	amount := recentRevision.NewValidProofOutputs[0].Value.Sub(renterRevision.NewValidProofOutputs[0].Value)
	var deposit types.Currency
	if cost.Cmp(amount) <= 0 {
		deposit = amount.Sub(cost)
	}
	if deposit.IsZero() {
		return failTo("ProcessFundEphemeralAccountRPC", "verify payment", modules.ErrInsufficientPaymentForRPC)
	}

	// create a sync chan to pass to the account manager, once the FC is fully
	// fsynced we'll close this so the account manager can properly lower the
	// host's outstanding risk induced by the (immediate) deposit.
	syncChan := make(chan struct{})
	err = p.h.staticAccountManager.callDeposit(accountID, deposit, syncChan)
	if err != nil {
		return failTo("ProcessFundEphemeralAccountRPC", "verify revision", err)
	}

	// update the storage obligation
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{renterRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// modify the storage obligation
	err = p.h.modifyStorageObligation(so, nil, nil, nil)
	if err != nil {
		p.h.log.Critical(fmt.Sprintf("Host incurred a loss of %v, ephemeral account funded but could not modify storage obligation, error %v", amount.HumanString(), err))
	}
	close(syncChan) // signal FC fsync by closing the sync channel

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	})
	if err != nil {
		return failTo("ProcessPaymentForRPC", "send response", err)
	}

	return amount, nil
}

// payByEphemeralAccount processes a PayByEphemeralAccountRequest coming in over
// the given stream.
func (p *paymentProcessor) payByEphemeralAccount(stream siamux.Stream) (types.Currency, error) {
	// read the PayByEphemeralAccountRequest
	var pbear modules.PayByEphemeralAccountRequest
	if err := modules.RPCRead(stream, &pbear); err != nil {
		return failTo("ProcessPaymentForRPC", "read PayByEphemeralAccountRequest", err)
	}
	// process the request
	err := p.h.staticAccountManager.callWithdraw(&pbear.Message, pbear.Signature, pbear.Priority)

	// send the response
	err = modules.RPCWrite(stream, modules.PayByEphemeralAccountResponse{
		Amount: pbear.Message.Amount,
	})
	if err != nil {
		return failTo("ProcessPaymentForRPC", "send PayByEphemeralAccountResponse", err)
	}
	return pbear.Message.Amount, nil
}

// payByContract processese a PayByContractRequest coming in over the given
// stream.
func (p *paymentProcessor) payByContract(stream siamux.Stream) (types.Currency, error) {
	// read the PayByContractRequest
	var pbcr modules.PayByContractRequest
	if err := modules.RPCRead(stream, &pbcr); err != nil {
		return failTo("ProcessPaymentForRPC", "read PayByContractRequest", err)
	}

	// lock the storage obligation
	so, err := p.lockStorageObligation(pbcr.ContractID)
	defer p.h.managedUnlockStorageObligation(so.id())
	if err != nil {
		return failTo("ProcessPaymentForRPC", "lock storage obligation", err)
	}

	// extract the proposed revision and the signature from the request
	recentRevision := so.recentRevision()
	renterRevision := revisionFromRequest(recentRevision, pbcr)
	renterSignature := signatureFromRequest(recentRevision, pbcr)

	// sign the revision
	blockHeight := p.h.BlockHeight()
	txn, err := createRevisionSignature(renterRevision, renterSignature, p.staticSecretKey, blockHeight)
	if err != nil {
		return failTo("ProcessPaymentForRPC", "verify revision", err)
	}

	// extract the payment output & update the storage obligation with the
	// host's signature
	amount := recentRevision.NewValidProofOutputs[0].Value.Sub(renterRevision.NewValidProofOutputs[0].Value)
	so.RevisionTransactionSet = []types.Transaction{{
		FileContractRevisions: []types.FileContractRevision{renterRevision},
		TransactionSignatures: []types.TransactionSignature{renterSignature, txn.TransactionSignatures[1]},
	}}

	// update the storage obligation
	p.h.mu.Lock()
	if err = p.h.modifyStorageObligation(so, nil, nil, nil); err != nil {
		p.h.mu.Unlock()
		return failTo("ProcessPaymentForRPC", "modify storage obligation", err)
	}
	p.h.mu.Unlock()

	// send the response
	var sig crypto.Signature
	copy(sig[:], txn.HostSignature().Signature[:])
	err = modules.RPCWrite(stream, modules.PayByContractResponse{
		Signature: sig,
	})
	if err != nil {
		return failTo("ProcessPaymentForRPC", "send response", err)
	}

	return amount, nil
}

// lockStorageObligation will call upon the host to lock the storage obligation
// for given ID. It returns the most recent revision and its signatures.
func (p *paymentProcessor) lockStorageObligation(fcid types.FileContractID) (so storageObligation, err error) {
	p.h.managedLockStorageObligation(fcid)

	// fetch the storage obligation, which has the revision, which has the
	// renter's public key.
	if so, err = p.h.managedGetStorageObligation(fcid); err != nil {
		err = extendErr("could not fetch "+fcid.String()+": ", ErrorInternal(err.Error()))
		return storageObligation{}, err
	}
	return
}

// revisionFromRequest creates a copy of the recent revision and decorates it
// with the suggested revision values which are provided through the
// PayByContractRequest object.
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

// signatureFromRequest creates a copy of the recent revision and decorates it
// with the signature provided through the PayByContractRequest object.
func signatureFromRequest(recent types.FileContractRevision, pbcr modules.PayByContractRequest) types.TransactionSignature {
	txn := types.NewTransaction(recent, 0)
	txn.TransactionSignatures[0].Signature = pbcr.Signature
	return txn.TransactionSignatures[0]
}

// failTo is a helper function that provides consistent context to the given
// error and returns it alongside a ZeroCurrency.
func failTo(method, action string, err error) (types.Currency, error) {
	return types.ZeroCurrency, errors.AddContext(err, fmt.Sprintf("Failed to %s, could not %s", method, action))
}
