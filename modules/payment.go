package modules

import (
	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/errors"
)

const (
	// WithdrawalNonceSize is the size of the nonce in the WithdralMessage
	WithdrawalNonceSize = 8
)

var (
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

type (
	// WithdrawalMessage contains all details to spend from an ephemeral account
	WithdrawalMessage struct {
		Account string
		Expiry  types.BlockHeight
		Amount  types.Currency
		Nonce   [WithdrawalNonceSize]byte
	}
)

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
	var spk types.SiaPublicKey
	spk.LoadString(wm.Account)
	var pk crypto.PublicKey
	copy(pk[:], spk.Key)

	err := crypto.VerifyHash(hash, pk, sig)
	if err != nil {
		return errors.Extend(err, ErrWithdrawalInvalidSignature)
	}
	return nil
}
