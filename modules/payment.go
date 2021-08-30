package modules

import (
	"io"

	"gitlab.com/NebulousLabs/errors"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
	"go.sia.tech/siad/build"
	"go.sia.tech/siad/crypto"
	"go.sia.tech/siad/types"
)

const (
	// WithdrawalNonceSize is the size of the nonce in the WithdralMessage
	WithdrawalNonceSize = 8
)

var (
	// ErrUnknownPaymentMethod occurs when the payment method specified in the
	// PaymentRequest object is unknown. The possible options are outlined below
	// under "Payment identifiers".
	ErrUnknownPaymentMethod = errors.New("unknown payment method")

	// ErrInvalidAccount occurs when the account specified in the WithdrawalMessage
	// is invalid.
	ErrInvalidAccount = errors.New("invalid account")
	// ErrInvalidPaymentMethod occurs when the payment method is not accepted
	// for a specific RPC.
	ErrInvalidPaymentMethod = errors.New("invalid payment method")

	// ErrInsufficientPaymentForRPC is returned when the provided payment was
	// lower than the cost of the RPC.
	ErrInsufficientPaymentForRPC = errors.New("Insufficient payment, the provided payment did not cover the cost of the RPC.")

	// ErrExpiredRPCPriceTable is returned when the renter performs an RPC call
	// and the current block height exceeds the expiry block height of the RPC
	// price table.
	ErrExpiredRPCPriceTable = errors.New("Expired RPC price table, ensure you have the latest prices by calling the updatePriceTable RPC.")

	// ErrWithdrawalsInactive occurs when the host is not synced yet. If that is
	// the case the account manager does not allow trading money from the
	// ephemeral accounts.
	ErrWithdrawalsInactive = errors.New("ephemeral account withdrawals are inactive because the host is not synced")

	// ErrWithdrawalExpired occurs when the withdrawal message's expiry block
	// height is in the past.
	ErrWithdrawalExpired = errors.New("ephemeral account withdrawal message expired")

	// ErrWithdrawalExtremeFuture occurs when the withdrawal message's expiry
	// block height is too far into the future.
	ErrWithdrawalExtremeFuture = errors.New("ephemeral account withdrawal message expires too far into the future")

	// ErrWithdrawalInvalidSignature occurs when the signature provided with the
	// withdrawal message was invalid.
	ErrWithdrawalInvalidSignature = errors.New("ephemeral account withdrawal message signature is invalid")
)

// PaymentProcessor is the interface implemented to handle RPC payments.
type PaymentProcessor interface {
	// ProcessPayment takes a stream and handles the payment request objects
	// sent by the caller. Returns an object that implements the PaymentDetails
	// interface, or an error in case of failure.
	ProcessPayment(stream siamux.Stream, bh types.BlockHeight) (PaymentDetails, error)
}

// PaymentDetails is an interface that defines method that give more information
// about the details of a processed payment.
type PaymentDetails interface {
	AccountID() AccountID
	Amount() types.Currency
}

// Payment identifiers
var (
	PayByContract         = types.NewSpecifier("PayByContract")
	PayByEphemeralAccount = types.NewSpecifier("PayByEphemAcc")
)

// ZeroAccountID is the only account id that is allowed to be invalid.
var ZeroAccountID = AccountID{""}

type (
	// AccountID is the unique identifier of an ephemeral account on the host.
	// It should always be a valid representation of types.SiaPublicKey or an
	// empty string.
	AccountID struct {
		spk string
	}

	// PaymentRequest identifies the payment method. This can be either
	// PayByContract or PayByEphemeralAccount
	PaymentRequest struct {
		Type types.Specifier
	}

	// PayByEphemeralAccountRequest holds all payment details to pay from an
	// ephemeral account.
	PayByEphemeralAccountRequest struct {
		Message   WithdrawalMessage
		Signature crypto.Signature
		Priority  int64
	}

	// PayByContractRequest holds all payment details to pay from a file
	// contract.
	PayByContractRequest struct {
		ContractID           types.FileContractID
		NewRevisionNumber    uint64
		NewValidProofValues  []types.Currency
		NewMissedProofValues []types.Currency
		RefundAccount        AccountID
		Signature            []byte
	}

	// PayByContractResponse is the object sent in response to the
	// PayByContractRequest
	PayByContractResponse struct {
		Signature crypto.Signature
	}

	// WithdrawalMessage contains all details to spend from an ephemeral account
	WithdrawalMessage struct {
		Account AccountID
		Expiry  types.BlockHeight
		Amount  types.Currency
		Nonce   [WithdrawalNonceSize]byte
	}

	// Receipt is returned by the host after a successful deposit into an
	// ephemeral account and can be used as proof of payment.
	Receipt struct {
		Host      types.SiaPublicKey
		Account   AccountID
		Amount    types.Currency
		Timestamp int64
	}
)

// NewAccountID is a helper function that creates a new account ID from a
// randomly generate key pair
func NewAccountID() (id AccountID, sk crypto.SecretKey) {
	var pk crypto.PublicKey
	sk, pk = crypto.GenerateKeyPair()
	id.FromSPK(types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	})
	return
}

// FromSPK creates an AccountID from a SiaPublicKey. This assumes that the
// provided key is valid and won't perform additional checks.
func (aid *AccountID) FromSPK(spk types.SiaPublicKey) {
	if spk.Equals(types.SiaPublicKey{}) {
		*aid = ZeroAccountID
		return
	}
	*aid = AccountID{spk.String()}
}

// IsZeroAccount returns whether or not the account id matches the empty string.
func (aid AccountID) IsZeroAccount() bool {
	return aid == ZeroAccountID
}

// LoadString loads an account id from a string.
func (aid *AccountID) LoadString(s string) error {
	var spk types.SiaPublicKey
	err := spk.LoadString(s)
	if err != nil {
		return errors.AddContext(err, "failed to load account id from string")
	}
	aid.FromSPK(spk)
	return nil
}

// MarshalSia implements the SiaMarshaler interface.
func (aid AccountID) MarshalSia(w io.Writer) error {
	if aid.IsZeroAccount() {
		return types.SiaPublicKey{}.MarshalSia(w)
	}
	return aid.SPK().MarshalSia(w)
}

// UnmarshalSia implements the SiaMarshaler interface.
func (aid *AccountID) UnmarshalSia(r io.Reader) error {
	var spk types.SiaPublicKey
	err := spk.UnmarshalSia(r)
	if err != nil {
		return err
	}
	aid.FromSPK(spk)
	return err
}

// SPK returns the account id as a types.SiaPublicKey.
func (aid AccountID) SPK() (spk types.SiaPublicKey) {
	if aid.IsZeroAccount() {
		build.Critical("should never use the zero account")
	}
	err := spk.LoadString(aid.spk)
	if err != nil {
		build.Critical("account id should never fail to be loaded as a SiaPublicKey")
	}
	return
}

// Validate checks the WithdrawalMessage's expiry and signature. If the
// signature is invalid, or if the WithdrawlMessage is already expired, or it
// expires too far into the future, an error is returned.
func (wm *WithdrawalMessage) Validate(blockHeight, expiry types.BlockHeight, hash crypto.Hash, sig crypto.Signature) error {
	return errors.Compose(
		wm.ValidateExpiry(blockHeight, expiry),
		wm.ValidateSignature(hash, sig),
	)
}

// ValidateExpiry returns an error if the withdrawal message is either already
// expired or if it expires too far into the future
func (wm *WithdrawalMessage) ValidateExpiry(blockHeight, expiry types.BlockHeight) error {
	// Verify the current blockheight does not exceed the expiry
	if blockHeight > wm.Expiry {
		return ErrWithdrawalExpired
	}
	// Verify the withdrawal is not too far into the future
	if wm.Expiry > expiry {
		return ErrWithdrawalExtremeFuture
	}
	return nil
}

// ValidateSignature returns an error if the provided signature is invalid
func (wm *WithdrawalMessage) ValidateSignature(hash crypto.Hash, sig crypto.Signature) error {
	var pk crypto.PublicKey
	if wm.Account.IsZeroAccount() {
		return errors.AddContext(ErrInvalidAccount, "cannot withdraw from zero account")
	}
	spk := wm.Account.SPK()
	if len(spk.Key) != crypto.PublicKeySize {
		return errors.AddContext(ErrInvalidAccount, "incorrect public key size")
	}
	copy(pk[:], spk.Key)

	err := crypto.VerifyHash(hash, pk, sig)
	if err != nil {
		return errors.Compose(err, ErrWithdrawalInvalidSignature)
	}
	return nil
}

// NewPayByEphemeralAccountRequest uses the given parameters to create a
// PayByEphemeralAccountRequest
func NewPayByEphemeralAccountRequest(account AccountID, expiry types.BlockHeight, amount types.Currency, sk crypto.SecretKey) PayByEphemeralAccountRequest {
	// generate a nonce
	var nonce [WithdrawalNonceSize]byte
	fastrand.Read(nonce[:])

	// create a new WithdrawalMessage
	wm := WithdrawalMessage{
		Account: account,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}

	// sign it
	sig := crypto.SignHash(crypto.HashObject(wm), sk)
	return PayByEphemeralAccountRequest{
		Message:   wm,
		Signature: sig,
	}
}
