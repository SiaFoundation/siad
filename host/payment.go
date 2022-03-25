package host

import (
	"errors"
	"fmt"
	"time"

	"go.sia.tech/core/net/rhp"
	"go.sia.tech/core/net/rpc"
	"go.sia.tech/core/types"
)

func validateEAWithdrawal(req rhp.PayByEphemeralAccountRequest, height uint64) (types.Hash256, error) {
	// verify the signature is correct.
	sigHash := req.Message.SigHash()
	if !req.Message.AccountID.VerifyHash(sigHash, req.Signature) {
		return types.Hash256{}, errors.New("withdrawal request signature is invalid")
	}

	switch {
	case req.Message.Expiry < height:
		return types.Hash256{}, errors.New("withdrawal request expired")
	case req.Message.Expiry > height+20:
		return types.Hash256{}, errors.New("withdrawal request too far in the future")
	case req.Message.Amount.IsZero():
		return types.Hash256{}, errors.New("withdrawal request has zero amount")
	}
	return sigHash, nil
}

// processEAPayment processes a payment using an ephemeral account.
func (r *rpcSession) processEAPayment() error {
	height := r.cm.Tip().Height

	var req rhp.PayByEphemeralAccountRequest
	if err := rpc.ReadObject(r.stream, &req); err != nil {
		return fmt.Errorf("failed to read EA payment request: %w", err)
	}

	withdrawID, err := validateEAWithdrawal(req, height)
	if err != nil {
		return fmt.Errorf("invalid EA payment request: %w", err)
	}

	// withdraw the funds from the account.
	_, err = r.accounts.Debit(req.Message.AccountID, withdrawID, req.Message.Amount)
	if err != nil {
		return fmt.Errorf("failed to withdraw from ephemeral account: %w", err)
	}

	// increase the budget and set the refund account ID.
	r.budget.Increase(req.Message.Amount)
	r.refundAccount = req.Message.AccountID
	return nil
}

// processContractPayment processes a payment using an existing contract.
func (r *rpcSession) processContractPayment() error {
	var req rhp.PayByContractRequest
	if err := rpc.ReadObject(r.stream, &req); err != nil {
		return fmt.Errorf("failed to read contract payment request: %w", err)
	}

	contract, err := r.contracts.Lock(req.ContractID, time.Second*30)
	if err != nil {
		return fmt.Errorf("failed to lock contract %v: %w", req.ContractID, err)
	}
	defer r.contracts.Unlock(req.ContractID)

	// calculate the fund amount as the difference between the new and old
	// valid host outputs.
	fundAmount, underflow := req.NewOutputs.HostValue.SubWithUnderflow(contract.Revision.HostOutput.Value)
	if underflow {
		return errors.New("new valid host value must be greater than current")
	}
	// create a new revision with updated output values and renter signature
	paymentRevision := contract.Revision
	paymentRevision.RevisionNumber = req.NewRevisionNumber
	req.NewOutputs.Apply(&paymentRevision)

	vc := r.cm.TipContext()
	sigHash := vc.ContractSigHash(paymentRevision)
	// validate the renter's signature and the payment revision
	if !contract.Revision.RenterPublicKey.VerifyHash(sigHash, req.Signature) {
		return errors.New("payment revision renter signature is invalid")
	} else if err := rhp.ValidatePaymentRevision(contract.Revision, paymentRevision, fundAmount); err != nil {
		return fmt.Errorf("invalid payment revision: %w", err)
	}

	// sign the new revision and apply the renter's signature.
	paymentRevision.HostSignature = r.privkey.SignHash(sigHash)
	paymentRevision.RenterSignature = req.Signature
	contract.Revision = paymentRevision

	// update the contract.
	if err := r.contracts.Revise(contract); err != nil {
		return fmt.Errorf("failed to update stored contract revision: %w", err)
	}

	// send the updated host signature to the renter
	err = rpc.WriteResponse(r.stream, &rhp.RPCRevisionSigningResponse{
		Signature: paymentRevision.HostSignature,
	})
	if err != nil {
		return fmt.Errorf("failed to send host signature response: %w", err)
	}

	// increase the budget and set the refund account ID.
	r.budget.Increase(fundAmount)
	r.refundAccount = req.RefundAccount
	return nil
}

// processPayment processes a payment request from the renter, sets the refund
// account and adds it to the session's budget.
func (r *rpcSession) processPayment() error {
	var req rpc.Specifier
	if err := rpc.ReadRequest(r.stream, &req); err != nil {
		return fmt.Errorf("failed to read payment request: %w", err)
	}

	switch req {
	case rhp.PayByEphemeralAccount:
		return r.processEAPayment()
	case rhp.PayByContract:
		return r.processContractPayment()
	default:
		return fmt.Errorf("unknown payment type %v", req)
	}
}
