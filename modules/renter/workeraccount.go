package renter

import (
	"sync"

	"gitlab.com/NebulousLabs/Sia/crypto"
	"gitlab.com/NebulousLabs/Sia/modules"
	"gitlab.com/NebulousLabs/Sia/types"
	"gitlab.com/NebulousLabs/fastrand"
	"gitlab.com/NebulousLabs/siamux"
)

// TODO: try to load account from persistence
//
// TODO: for now the account is a separate object that sits as first class
// object on the worker, most probably though this will move as to not have two
// separate mutex domains.

// withdrawalValidityPeriod defines the period (in blocks) a withdrawal message
// remains spendable after it has been created. Together with the current block
// height at time of creation, this period makes up the WithdrawalMessage's
// expiry block height.
const withdrawalValidityPeriod = 6

// account represents a renter's ephemeral account on a host.
type account struct {
	staticID        modules.AccountID
	staticHostKey   types.SiaPublicKey
	staticSecretKey crypto.SecretKey

	pendingSpends types.Currency
	pendingFunds  types.Currency
	balance       types.Currency

	mu sync.Mutex
	c  hostContractor
}

// openAccount returns an account for the given host. In the case it does
// not exist yet, it gets created. Every time a new account is created, a new
// keypair is used.
func openAccount(hostKey types.SiaPublicKey) *account {
	// generate a new key pair
	sk, pk := crypto.GenerateKeyPair()
	spk := types.SiaPublicKey{
		Algorithm: types.SignatureEd25519,
		Key:       pk[:],
	}

	var aid modules.AccountID
	aid.FromSPK(spk)

	// create the account
	return &account{
		staticID:        aid,
		staticHostKey:   hostKey,
		staticSecretKey: sk,
	}
}

// AvailableBalance returns the amount of money that is available to spend. It
// is calculated by taking into account pending spends and pending funds.
func (a *account) AvailableBalance() types.Currency {
	a.mu.Lock()
	defer a.mu.Unlock()

	total := a.balance.Add(a.pendingFunds)
	if a.pendingSpends.Cmp(total) < 0 {
		return total.Sub(a.pendingSpends)
	}
	return types.ZeroCurrency
}

// ProvidePayment takes a stream and various payment details and handles the
// payment by sending and processing payment request and response objects.
// Returns an error in case of failure.
func (a *account) ProvidePayment(stream siamux.Stream, host types.SiaPublicKey, rpc types.Specifier, amount types.Currency, blockHeight types.BlockHeight) error {
	// NOTE: we purposefully do not verify if the account has sufficient funds.
	// Seeing as withdrawals are a blocking action on the host, it is perfectly
	// ok to trigger them from an account with insufficient balance.

	// create a withdrawal message
	msg := a.createWithdrawalMessage(amount, blockHeight+withdrawalValidityPeriod)
	sig := crypto.SignHash(crypto.HashObject(msg), a.staticSecretKey)

	// send PaymentRequest
	err := modules.RPCWrite(stream, modules.PaymentRequest{Type: modules.PayByEphemeralAccount})
	if err != nil {
		return err
	}

	// send PayByEphemeralAccountRequest
	err = modules.RPCWrite(stream, modules.PayByEphemeralAccountRequest{
		Message:   msg,
		Signature: sig,
	})
	if err != nil {
		return err
	}

	// receive PayByEphemeralAccountResponse
	var payByResponse modules.PayByEphemeralAccountResponse
	err = modules.RPCRead(stream, &payByResponse)
	if err != nil {
		return err
	}
	return nil
}

// createWithdrawalMessage is a helper function that takes a set of parameters
// and a WithdrawalMessage.
func (a *account) createWithdrawalMessage(amount types.Currency, expiry types.BlockHeight) modules.WithdrawalMessage {
	var nonce [modules.WithdrawalNonceSize]byte
	copy(nonce[:], fastrand.Bytes(len(nonce)))
	return modules.WithdrawalMessage{
		Account: a.staticID,
		Expiry:  expiry,
		Amount:  amount,
		Nonce:   nonce,
	}
}
