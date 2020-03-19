package host

import (
	"time"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/siamux"
)

// managedRPCFundEphemeralAccount handles the RPC request from the renter to
// fund its ephemeral account.
func (h *Host) managedRPCFundEphemeralAccount(stream siamux.Stream, pt *modules.RPCPriceTable) error {
	// read the FundAccountRequest
	var far modules.FundAccountRequest
	if err := modules.RPCRead(stream, &far); err != nil {
		return errors.AddContext(err, "Could not read FundEphemeralAccountRequest")
	}

	// read the PaymentRequest and ensure it's a PayByContract request; for now
	// it does not make sense to fund an ephemeral account by anything but a
	// file contract - we might enable this in the future
	var pr modules.PaymentRequest
	if err := modules.RPCRead(stream, &pr); err != nil {
		return errors.AddContext(err, "Could not read PaymentRequest")
	}
	if pr.Type != modules.PayByContract {
		return errors.AddContext(modules.ErrInvalidPaymentMethod, "Funding an ephemeral account is done through PayByContract")
	}

	// fund the account
	funded, err := h.managedFundByContract(stream, far, pt.FundAccountCost)
	if err != nil {
		return errors.AddContext(err, "Funding ephemeral failed")
	}

	// There's no need to verify payment here. The account get funded by the
	// amount paid minus the cost of the RPC. If the amount paid did not cover
	// the cost of the RPC, an error will have been returned.

	// create the receipt and sign it
	receipt := modules.Receipt{
		Host:      h.PublicKey(),
		Account:   far.AccountID,
		Amount:    funded,
		Timestamp: time.Now().Unix(),
	}
	signature := crypto.SignHash(crypto.HashObject(receipt), h.secretKey)

	// send the FundAccountResponse
	if err = modules.RPCWrite(stream, modules.FundAccountResponse{
		Receipt:   receipt,
		Signature: signature[:],
	}); err != nil {
		return errors.AddContext(err, "Failed to send FundAccountResponse")
	}

	return nil
}

// managedCalculateFundEphemeralAccountCost calculates the price for the
// FundEphemeralAccountRPC. The price can be dependant on numerous factors.
// Note: for now this is a fixed cost equaling the base RPC price.
func (h *Host) managedCalculateFundEphemeralAccountCost() types.Currency {
	hIS := h.InternalSettings()
	return hIS.MinBaseRPCPrice
}
